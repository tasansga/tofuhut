package cmd

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"tofuhut/internal/reconciler"
	"tofuhut/internal/reconciler/scheduler"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage the approval and reconciliation server",
}

var serverRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the approval and reconciliation server",
	RunE: func(cmd *cobra.Command, args []string) error {
		listen, err := cmd.Flags().GetString("listen")
		if err != nil {
			return err
		}
		enableScheduler, err := cmd.Flags().GetBool("enable-scheduler")
		if err != nil {
			return err
		}
		defaultInterval, err := cmd.Flags().GetDuration("scheduler-default-interval")
		if err != nil {
			return err
		}
		jitter, err := cmd.Flags().GetDuration("scheduler-jitter")
		if err != nil {
			return err
		}
		maxConcurrent, err := cmd.Flags().GetInt("scheduler-max-concurrent")
		if err != nil {
			return err
		}
		rescanInterval, err := cmd.Flags().GetDuration("scheduler-rescan-interval")
		if err != nil {
			return err
		}

		cfg := reconciler.Config{}
		locks := reconciler.ConfigLocks{}
		handler := newApproveHandler(cfg, locks)
		server := &http.Server{
			Addr:              listen,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		}

		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		hupCh := make(chan os.Signal, 1)
		signal.Notify(hupCh, syscall.SIGHUP)
		defer signal.Stop(hupCh)

		errCh := make(chan error, 1)
		go func() {
			logrus.Infof("server listening on %s", listen)
			errCh <- server.ListenAndServe()
		}()

		var sched *scheduler.Scheduler
		var schedCancel context.CancelFunc
		startScheduler := func(specs []reconciler.WorkloadSpec) {
			if schedCancel != nil {
				schedCancel()
			}
			if sched != nil {
				sched.Wait()
			}
			if len(specs) == 0 {
				sched = nil
				schedCancel = nil
				return
			}
			childCtx, childCancel := context.WithCancel(ctx)
			schedCancel = childCancel
			runner := reconciler.NewDefaultRunner(cfg, locks)
			sched = scheduler.New(runner, toSchedulerSpecs(specs), scheduler.Options{
				Jitter:        jitter,
				MaxConcurrent: maxConcurrent,
			})
			sched.Start(childCtx)
		}

		if enableScheduler {
			specs, err := loadValidWorkloads(defaultInterval, cfg, locks)
			if err != nil {
				return err
			}
			startScheduler(specs)
		}

		if enableScheduler && rescanInterval > 0 {
			go func() {
				ticker := time.NewTicker(rescanInterval)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-hupCh:
						logrus.Info("reload requested; rescanning workloads")
					case <-ticker.C:
						logrus.Info("rescan interval reached; rescanning workloads")
					}

					specs, err := loadValidWorkloads(defaultInterval, cfg, locks)
					if err != nil {
						logrus.WithError(err).Warn("failed to rescan workloads")
						continue
					}
					startScheduler(specs)
				}
			}()
		}

		select {
		case <-ctx.Done():
		case err := <-errCh:
			if err != nil && err != http.ErrServerClosed {
				return err
			}
		}

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logrus.WithError(err).Warn("server shutdown failed")
		}
		if sched != nil {
			sched.Wait()
		}
		return nil
	},
}

func init() {
	serverRunCmd.Flags().String("listen", ":8080", "Listen address for server")
	serverRunCmd.Flags().Bool("enable-scheduler", false, "Enable periodic workload reconciliation")
	serverRunCmd.Flags().Duration("scheduler-default-interval", 0, "Default reconciliation interval (per workload override via RECONCILE_INTERVAL)")
	serverRunCmd.Flags().Duration("scheduler-jitter", 0, "Add up to this much jitter to each interval")
	serverRunCmd.Flags().Int("scheduler-max-concurrent", 2, "Maximum concurrent reconciliations (0 = unlimited)")
	serverRunCmd.Flags().Duration("scheduler-rescan-interval", 5*time.Minute, "Rescan workloads on this interval (0 = disable)")
	serverCmd.AddCommand(serverRunCmd)
	rootCmd.AddCommand(serverCmd)
}

type approveHandler struct {
	cfg   reconciler.Config
	locks reconciler.ConfigLocks
}

func newApproveHandler(cfg reconciler.Config, locks reconciler.ConfigLocks) http.Handler {
	return &approveHandler{cfg: cfg, locks: locks}
}

func (h *approveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		logrus.WithFields(logrus.Fields{
			"method": r.Method,
			"path":   r.URL.Path,
		}).Warn("approve request rejected: method not allowed")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/approve/") {
		logrus.WithFields(logrus.Fields{
			"method": r.Method,
			"path":   r.URL.Path,
		}).Warn("approve request rejected: not found")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	workload := strings.TrimPrefix(r.URL.Path, "/approve/")
	if workload == "" || strings.Contains(workload, "/") {
		logrus.WithFields(logrus.Fields{
			"path": r.URL.Path,
		}).Warn("approve request rejected: invalid workload path")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := validateWorkloadName(workload); err != nil {
		logrus.WithFields(logrus.Fields{
			"workload": workload,
		}).Warn("approve request rejected: invalid workload name")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	effectiveToken, err := tokenFromWorkloadEnv(workload, h.cfg, h.locks)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"workload": workload,
		}).Error("approve request failed: env token lookup error")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if effectiveToken != "" {
		if auth := r.Header.Get("Authorization"); auth != "Bearer "+effectiveToken {
			logrus.WithFields(logrus.Fields{
				"workload": workload,
			}).Warn("approve request rejected: unauthorized")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	workdir := reconciler.WorkDirPath(workload)
	if _, err := os.Stat(workdir); err != nil {
		if os.IsNotExist(err) {
			logrus.WithFields(logrus.Fields{
				"workload": workload,
			}).Warn("approve request rejected: workload directory not found")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		logrus.WithFields(logrus.Fields{
			"workload": workload,
		}).Error("approve request failed: workload directory stat error")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	planPath := filepath.Join(workdir, "plan.tfplan")
	if _, err := os.Stat(planPath); err != nil {
		if os.IsNotExist(err) {
			logrus.WithFields(logrus.Fields{
				"workload": workload,
			}).Warn("approve request rejected: plan not found")
			w.WriteHeader(http.StatusConflict)
			return
		}
		logrus.WithFields(logrus.Fields{
			"workload": workload,
		}).Error("approve request failed: plan stat error")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	approvePath := filepath.Join(workdir, "approve")
	if err := os.WriteFile(approvePath, []byte("approved"), 0600); err != nil {
		logrus.WithFields(logrus.Fields{
			"workload": workload,
		}).Error("approve request failed: write approve file")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	logrus.WithFields(logrus.Fields{
		"workload": workload,
		"path":     approvePath,
		"latency":  time.Since(start).String(),
	}).Info("approval recorded")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
}

func tokenFromWorkloadEnv(workload string, cfg reconciler.Config, locks reconciler.ConfigLocks) (string, error) {
	envFile := reconciler.EnvFilePath(workload)
	envFromFile, err := reconciler.LoadEnvFile(envFile)
	if err != nil {
		return "", err
	}
	merged, err := reconciler.MergeConfig(cfg, locks, envFromFile)
	if err != nil {
		return "", err
	}
	return merged.ApproveToken, nil
}

func toSchedulerSpecs(specs []reconciler.WorkloadSpec) []scheduler.WorkloadSpec {
	out := make([]scheduler.WorkloadSpec, 0, len(specs))
	for _, spec := range specs {
		out = append(out, scheduler.WorkloadSpec{
			Name:     spec.Name,
			Interval: spec.Interval,
			Enabled:  spec.Enabled,
		})
	}
	return out
}

func loadValidWorkloads(defaultInterval time.Duration, cfg reconciler.Config, locks reconciler.ConfigLocks) ([]reconciler.WorkloadSpec, error) {
	specs, err := reconciler.LoadWorkloadSpecs(defaultInterval)
	if err != nil {
		return nil, err
	}

	valid, problems := filterValidWorkloads(specs, cfg, locks)
	for _, problem := range problems {
		logrus.Warn(problem)
	}
	return valid, nil
}

func filterValidWorkloads(specs []reconciler.WorkloadSpec, cfg reconciler.Config, locks reconciler.ConfigLocks) ([]reconciler.WorkloadSpec, []string) {
	var problems []string
	valid := make([]reconciler.WorkloadSpec, 0, len(specs))
	for _, spec := range specs {
		if !spec.Enabled || spec.Interval <= 0 {
			continue
		}
		envFile := reconciler.EnvFilePath(spec.Name)
		envFromFile, err := reconciler.LoadEnvFile(envFile)
		if err != nil {
			problems = append(problems, spec.Name+": "+err.Error())
			continue
		}
		merged, err := reconciler.MergeConfig(cfg, locks, envFromFile)
		if err != nil {
			problems = append(problems, spec.Name+": "+err.Error())
			continue
		}
		if merged.Mode == "apply" && merged.ApproveToken == "" {
			problems = append(problems, spec.Name+": MODE=apply requires APPROVE_TOKEN to be set")
			continue
		}
		valid = append(valid, spec)
	}
	return valid, problems
}
