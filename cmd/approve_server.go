package cmd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"tofuhut/internal/reconciler"
)

var approveServerCmd = &cobra.Command{
	Use:   "approve-server",
	Short: "Run an approval webhook server for ntfy actions",
	RunE: func(cmd *cobra.Command, args []string) error {
		listen, err := cmd.Flags().GetString("listen")
		if err != nil {
			return err
		}

		handler := newApproveHandler(resolvedConfig, resolvedConfigLocks)
		server := &http.Server{
			Addr:              listen,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		}

		logrus.Infof("approval server listening on %s", listen)
		return server.ListenAndServe()
	},
}

func init() {
	approveServerCmd.Flags().String("listen", ":8080", "Listen address for approval server")
	rootCmd.AddCommand(approveServerCmd)
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
