package cmd

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"tofuhut/internal/reconciler"
	"tofuhut/internal/reconciler/scheduler"
)

var errWorkloadLocked = errors.New("workload already running")
var errWorkloadDisabled = errors.New("workload disabled")

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

		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		cfg := reconciler.Config{}
		locks := reconciler.ConfigLocks{}
		dispatcher := newDispatcher(reconciler.NewDefaultRunner(cfg, locks), ctx)
		handler := newServerHandler(cfg, locks, dispatcher)
		handler.(*serverHandler).ctx = ctx
		server := &http.Server{
			Addr:              listen,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		}

		hupCh := make(chan os.Signal, 1)
		signal.Notify(hupCh, syscall.SIGHUP)
		defer signal.Stop(hupCh)

		errCh := make(chan error, 1)
		go func() {
			logrus.Infof("server listening on %s", listen)
			errCh <- server.ListenAndServe()
		}()

		var schedMu sync.Mutex
		var sched *scheduler.Scheduler
		var schedCancel context.CancelFunc
		startScheduler := func(specs []reconciler.WorkloadSpec) {
			schedMu.Lock()
			defer schedMu.Unlock()
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
			sched = scheduler.New(newTriggerRunner(dispatcher), toSchedulerSpecs(specs), scheduler.Options{
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
			dispatcher.DisableExcept(workloadsSet(specs))
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
					dispatcher.DisableExcept(workloadsSet(specs))
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
		dispatcher.Stop()
		schedMu.Lock()
		currentSched := sched
		schedMu.Unlock()
		if currentSched != nil {
			currentSched.Wait()
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

type serverHandler struct {
	cfg        reconciler.Config
	locks      reconciler.ConfigLocks
	dispatcher *dispatcher
	ctx        context.Context
}

func newServerHandler(cfg reconciler.Config, locks reconciler.ConfigLocks, dispatcher *dispatcher) http.Handler {
	return &serverHandler{
		cfg:        cfg,
		locks:      locks,
		dispatcher: dispatcher,
		ctx:        context.Background(),
	}
}

func (h *serverHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/approve/") {
		h.handleApprove(w, r, start)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/reconcile/") {
		h.handleReconcile(w, r, start)
		return
	}
	logrus.WithFields(logrus.Fields{
		"method": r.Method,
		"path":   r.URL.Path,
	}).Warn("request rejected: not found")
	w.WriteHeader(http.StatusNotFound)
}

func (h *serverHandler) handleApprove(w http.ResponseWriter, r *http.Request, start time.Time) {
	if r.Method != http.MethodPost {
		logrus.WithFields(logrus.Fields{
			"method": r.Method,
			"path":   r.URL.Path,
		}).Warn("approve request rejected: method not allowed")
		w.WriteHeader(http.StatusMethodNotAllowed)
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

	mergedCfg, err := workloadConfigFromEnv(workload, h.cfg, h.locks)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"workload": workload,
		}).Error("approve request failed: env token lookup error")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if mergedCfg.WorkloadToken != "" {
		if auth := r.Header.Get("Authorization"); auth != "Bearer "+mergedCfg.WorkloadToken {
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

	switch mergedCfg.WorkloadType {
	case "ansible":
		playbookPath := filepath.Join(workdir, "playbook.yml")
		if _, err := os.Stat(playbookPath); err != nil {
			if os.IsNotExist(err) {
				logrus.WithFields(logrus.Fields{
					"workload": workload,
				}).Warn("approve request rejected: playbook not found")
				w.WriteHeader(http.StatusConflict)
				return
			}
			logrus.WithFields(logrus.Fields{
				"workload": workload,
			}).Error("approve request failed: playbook stat error")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		approvePendingPath := filepath.Join(workdir, "approve.pending")
		if _, err := os.Stat(approvePendingPath); err != nil {
			if os.IsNotExist(err) {
				logrus.WithFields(logrus.Fields{
					"workload": workload,
				}).Warn("approve request rejected: no pending approval")
				w.WriteHeader(http.StatusConflict)
				return
			}
			logrus.WithFields(logrus.Fields{
				"workload": workload,
			}).Error("approve request failed: pending approval stat error")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	case "dnscontrol":
		dnsConfigPath := filepath.Join(workdir, "dnsconfig.js")
		if _, err := os.Stat(dnsConfigPath); err != nil {
			if os.IsNotExist(err) {
				logrus.WithFields(logrus.Fields{
					"workload": workload,
				}).Warn("approve request rejected: dnsconfig not found")
				w.WriteHeader(http.StatusConflict)
				return
			}
			logrus.WithFields(logrus.Fields{
				"workload": workload,
			}).Error("approve request failed: dnsconfig stat error")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		approvePendingPath := filepath.Join(workdir, "approve.pending")
		if _, err := os.Stat(approvePendingPath); err != nil {
			if os.IsNotExist(err) {
				logrus.WithFields(logrus.Fields{
					"workload": workload,
				}).Warn("approve request rejected: no pending approval")
				w.WriteHeader(http.StatusConflict)
				return
			}
			logrus.WithFields(logrus.Fields{
				"workload": workload,
			}).Error("approve request failed: pending approval stat error")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	default:
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

func (h *serverHandler) handleReconcile(w http.ResponseWriter, r *http.Request, start time.Time) {
	if r.Method != http.MethodPost {
		logrus.WithFields(logrus.Fields{
			"method": r.Method,
			"path":   r.URL.Path,
		}).Warn("reconcile request rejected: method not allowed")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.dispatcher == nil {
		logrus.Error("reconcile request failed: no dispatcher configured")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	workload := strings.TrimPrefix(r.URL.Path, "/reconcile/")
	if workload == "" || strings.Contains(workload, "/") {
		logrus.WithFields(logrus.Fields{
			"path": r.URL.Path,
		}).Warn("reconcile request rejected: invalid workload path")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := validateWorkloadName(workload); err != nil {
		logrus.WithFields(logrus.Fields{
			"workload": workload,
		}).Warn("reconcile request rejected: invalid workload name")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	mergedCfg, err := workloadConfigFromEnv(workload, h.cfg, h.locks)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"workload": workload,
		}).Error("reconcile request failed: env token lookup error")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if mergedCfg.WorkloadToken != "" {
		if auth := r.Header.Get("Authorization"); auth != "Bearer "+mergedCfg.WorkloadToken {
			logrus.WithFields(logrus.Fields{
				"workload": workload,
			}).Warn("reconcile request rejected: unauthorized")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	workdir := reconciler.WorkDirPath(workload)
	if _, err := os.Stat(workdir); err != nil {
		if os.IsNotExist(err) {
			logrus.WithFields(logrus.Fields{
				"workload": workload,
			}).Warn("reconcile request rejected: workload directory not found")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		logrus.WithFields(logrus.Fields{
			"workload": workload,
		}).Error("reconcile request failed: workload directory stat error")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err := h.dispatcher.Trigger(h.ctx, workload); err != nil {
		logrus.WithFields(logrus.Fields{
			"workload": workload,
			"error":    err.Error(),
		}).Warn("reconcile request rejected")
		switch err {
		case errWorkloadDisabled:
			w.WriteHeader(http.StatusConflict)
		default:
			w.WriteHeader(http.StatusLocked)
		}
		return
	}

	logrus.WithFields(logrus.Fields{
		"workload": workload,
		"latency":  time.Since(start).String(),
	}).Info("manual reconcile queued")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("accepted"))
}

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
}

type dispatcher struct {
	runner        reconciler.Runner
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	queues        map[string]*workloadQueue
	enabled       map[string]bool
	hasEnabledSet bool
	wg            sync.WaitGroup
}

func newDispatcher(runner reconciler.Runner, ctx context.Context) *dispatcher {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	return &dispatcher{
		runner:  runner,
		ctx:     ctx,
		cancel:  cancel,
		queues:  make(map[string]*workloadQueue),
		enabled: make(map[string]bool),
	}
}

func (d *dispatcher) Start() {
	// no-op: workers start lazily on first trigger
}

func (d *dispatcher) Stop() {
	d.cancel()
	d.wg.Wait()
}

func (d *dispatcher) Trigger(ctx context.Context, workload string) error {
	_, _, err := d.triggerWithResult(ctx, workload)
	return err
}

func (d *dispatcher) TriggerSync(ctx context.Context, workload string) error {
	result, _, err := d.triggerWithResult(ctx, workload)
	if err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *dispatcher) triggerWithResult(ctx context.Context, workload string) (chan error, chan struct{}, error) {
	d.mu.Lock()
	if d.hasEnabledSet {
		if !d.enabled[workload] {
			d.mu.Unlock()
			return nil, nil, errWorkloadDisabled
		}
	}
	queue, ok := d.queues[workload]
	if !ok {
		queue = &workloadQueue{
			ch:     make(chan struct{}, 1),
			done:   make(chan struct{}),
			result: make(chan error, 1),
			open:   true,
		}
		d.queues[workload] = queue
		d.startWorkerLocked(workload, queue)
	}
	if queue.disabled {
		d.mu.Unlock()
		return nil, nil, errWorkloadDisabled
	}
	if queue.inFlight || queue.pending {
		d.mu.Unlock()
		return nil, nil, errWorkloadLocked
	}
	queue.pending = true
	if !queue.open {
		queue.done = make(chan struct{})
		queue.result = make(chan error, 1)
		queue.open = true
	}
	queue.pendingCtx = ctx
	done := queue.done
	result := queue.result
	d.mu.Unlock()

	select {
	case queue.ch <- struct{}{}:
		return result, done, nil
	default:
		d.mu.Lock()
		queue.pending = false
		if queue.open {
			close(queue.result)
			close(queue.done)
			queue.open = false
		}
		d.mu.Unlock()
		return nil, nil, errWorkloadLocked
	}
}

func (d *dispatcher) DisableExcept(keep map[string]struct{}) {
	d.mu.Lock()
	d.hasEnabledSet = true
	d.enabled = make(map[string]bool, len(keep))
	for name := range keep {
		d.enabled[name] = true
	}
	for name, queue := range d.queues {
		_, ok := keep[name]
		queue.disabled = !ok
	}
	d.mu.Unlock()
}

func (d *dispatcher) Wait(workload string, timeout time.Duration) bool {
	d.mu.Lock()
	queue, ok := d.queues[workload]
	d.mu.Unlock()
	if !ok {
		return true
	}
	d.mu.Lock()
	if !queue.inFlight && !queue.pending {
		d.mu.Unlock()
		return true
	}
	done := queue.done
	open := queue.open
	d.mu.Unlock()
	if !open {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (d *dispatcher) startWorkerLocked(workload string, queue *workloadQueue) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			select {
			case <-d.ctx.Done():
				return
			case <-queue.ch:
			}
			d.mu.Lock()
			queue.pending = false
			queue.inFlight = true
			runCtx := queue.pendingCtx
			queue.pendingCtx = nil
			d.mu.Unlock()
			runStart := time.Now()
			if runCtx == nil {
				runCtx = d.ctx
			}
			if runCtx.Err() != nil {
				runErr := runCtx.Err()
				d.mu.Lock()
				queue.inFlight = false
				if queue.open {
					queue.result <- runErr
					close(queue.result)
					close(queue.done)
					queue.open = false
				}
				d.mu.Unlock()
				continue
			}
			err := d.runner.Run(runCtx, workload)
			d.mu.Lock()
			queue.inFlight = false
			if queue.open {
				queue.result <- err
				close(queue.result)
				close(queue.done)
				queue.open = false
			}
			d.mu.Unlock()
			if err != nil {
				logrus.WithFields(logrus.Fields{
					"workload": workload,
					"latency":  time.Since(runStart).String(),
					"error":    err.Error(),
				}).Warn("reconcile failed")
				continue
			}
			logrus.WithFields(logrus.Fields{
				"workload": workload,
				"latency":  time.Since(runStart).String(),
			}).Info("reconcile completed")
		}
	}()
}

type workloadQueue struct {
	ch         chan struct{}
	done       chan struct{}
	result     chan error
	open       bool
	pending    bool
	inFlight   bool
	disabled   bool
	pendingCtx context.Context
}

type triggerRunner struct {
	dispatcher *dispatcher
}

func newTriggerRunner(dispatcher *dispatcher) reconciler.Runner {
	return &triggerRunner{dispatcher: dispatcher}
}

func (r *triggerRunner) Run(ctx context.Context, workload string) error {
	if r.dispatcher == nil {
		return nil
	}
	return r.dispatcher.TriggerSync(ctx, workload)
}

func workloadConfigFromEnv(workload string, cfg reconciler.Config, locks reconciler.ConfigLocks) (reconciler.Config, error) {
	envFile := reconciler.EnvFilePath(workload)
	envFromFile, err := reconciler.LoadEnvFile(envFile)
	if err != nil {
		return reconciler.Config{}, err
	}
	merged, err := reconciler.MergeConfig(cfg, locks, envFromFile)
	if err != nil {
		return reconciler.Config{}, err
	}
	return merged, nil
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
		if err := reconciler.ValidateRuntime(merged); err != nil {
			problems = append(problems, spec.Name+": "+err.Error())
			continue
		}
		if merged.Mode == "apply" && merged.WorkloadToken == "" {
			problems = append(problems, spec.Name+": MODE=apply requires WORKLOAD_TOKEN to be set")
			continue
		}
		valid = append(valid, spec)
	}
	return valid, problems
}

func workloadsSet(specs []reconciler.WorkloadSpec) map[string]struct{} {
	set := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if spec.Enabled && spec.Interval > 0 {
			set[spec.Name] = struct{}{}
		}
	}
	return set
}
