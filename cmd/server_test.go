package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"tofuhut/internal/reconciler"
	"tofuhut/internal/reconciler/scheduler"
)

type fakeRunner struct {
	started  chan struct{}
	block    chan struct{}
	workload string
	force    bool
	trigger  string
}

func makePaths(t *testing.T) reconciler.Paths {
	t.Helper()
	return reconciler.Paths{
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
}

func writeWorkloadEnv(t *testing.T, paths reconciler.Paths, workload, content string) {
	t.Helper()
	envFile := filepath.Join(paths.ConfigDir, workload+".env")
	assert.NoError(t, os.WriteFile(envFile, []byte(content), 0644))
}

func newFakeRunner(block bool) *fakeRunner {
	r := &fakeRunner{
		started: make(chan struct{}, 1),
	}
	if block {
		r.block = make(chan struct{})
	}
	return r
}

func (r *fakeRunner) Run(ctx context.Context, workload string) error {
	r.workload = workload
	r.force = reconciler.ForceReconcileFromContext(ctx)
	r.trigger = reconciler.TriggerSourceFromContext(ctx)
	select {
	case r.started <- struct{}{}:
	default:
	}
	if r.block == nil {
		return nil
	}
	select {
	case <-r.block:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestApproveServerRejectsMissingPlan(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	writeWorkloadEnv(t, paths, "demo", "WORKLOAD_TYPE=tofu\n")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer token")

	h := newServerHandler(reconciler.Config{WorkloadType: "tofu", WorkloadToken: "token"}, reconciler.ConfigLocks{}, nil, paths)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestServeHTTPSetsRequestIDHeader(t *testing.T) {
	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, nil, makePaths(t))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
}

func TestApproveServerAllowsAnsibleWithoutPlan(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "playbook.yml"), []byte("ok"), 0644))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "approve.pending"), []byte("pending"), 0600))

	envFile := filepath.Join(paths.ConfigDir, "demo.env")
	assert.NoError(t, os.WriteFile(envFile, []byte("WORKLOAD_TYPE=ansible\nWORKLOAD_TOKEN=token\n"), 0644))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer token")

	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, nil, paths)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestApproveServerAllowsDNSControlWithoutPlan(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "dnsconfig.js"), []byte("ok"), 0644))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "approve.pending"), []byte("pending"), 0600))

	envFile := filepath.Join(paths.ConfigDir, "demo.env")
	assert.NoError(t, os.WriteFile(envFile, []byte("WORKLOAD_TYPE=dnscontrol\nWORKLOAD_TOKEN=token\n"), 0644))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer token")

	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, nil, paths)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestReconcileStartsWorkload(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	writeWorkloadEnv(t, paths, "demo", "WORKLOAD_TYPE=tofu\n")

	runner := newFakeRunner(false)
	dispatcher := newDispatcher(runner, context.Background())
	t.Cleanup(dispatcher.Stop)
	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, dispatcher, paths)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/reconcile/demo", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	select {
	case <-runner.started:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected runner to start")
	}
	assert.Equal(t, "demo", runner.workload)
	assert.True(t, runner.force)
	assert.Equal(t, "api_manual", runner.trigger)
}

func TestMetricsEndpointAvailable(t *testing.T) {
	paths := makePaths(t)

	metricsHandler, shutdownMetrics, err := setupMetrics()
	assert.NoError(t, err)
	t.Cleanup(func() {
		_ = shutdownMetrics(context.Background())
	})

	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, nil, paths).(*serverHandler)
	h.metricsHandler = metricsHandler

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, strings.Contains(rec.Body.String(), "# HELP"))
}

func TestReconcileRejectsUnauthorized(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	writeWorkloadEnv(t, paths, "demo", "WORKLOAD_TYPE=tofu\n")

	runner := newFakeRunner(false)
	dispatcher := newDispatcher(runner, context.Background())
	t.Cleanup(dispatcher.Stop)
	h := newServerHandler(reconciler.Config{WorkloadToken: "token"}, reconciler.ConfigLocks{}, dispatcher, paths)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/reconcile/demo", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestReconcileReturnsLockedWhenRunning(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	writeWorkloadEnv(t, paths, "demo", "WORKLOAD_TYPE=tofu\n")

	runner := newFakeRunner(true)
	dispatcher := newDispatcher(runner, context.Background())
	t.Cleanup(dispatcher.Stop)
	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, dispatcher, paths)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/reconcile/demo", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/reconcile/demo", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusLocked, rec.Code)

	close(runner.block)
	assert.True(t, dispatcher.Wait("demo", 500*time.Millisecond))
}

func TestApproveServerWritesApproveFile(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "plan.tfplan"), []byte("plan"), 0600))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer token")

	h := newServerHandler(reconciler.Config{WorkloadType: "tofu", WorkloadToken: "token"}, reconciler.ConfigLocks{}, nil, paths)
	h.ServeHTTP(rec, req)

	approvePath := filepath.Join(workdir, "approve")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.FileExists(t, approvePath)
}

func TestApproveServerUnauthorized(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "plan.tfplan"), []byte("plan"), 0600))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)

	h := newServerHandler(reconciler.Config{WorkloadType: "tofu", WorkloadToken: "token"}, reconciler.ConfigLocks{}, nil, paths)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestApproveServerAllowsWithoutToken(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "plan.tfplan"), []byte("plan"), 0600))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)

	h := newServerHandler(reconciler.Config{WorkloadType: "tofu"}, reconciler.ConfigLocks{}, nil, paths)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestApproveServerRejectsInvalidWorkload(t *testing.T) {
	paths := makePaths(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/../bad", nil)

	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, nil, paths)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestApproveServerRejectsDotWorkloads(t *testing.T) {
	paths := makePaths(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/..", nil)

	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, nil, paths)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestApproveServerUsesTokenFromEnvFile(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "plan.tfplan"), []byte("plan"), 0600))

	envFile := filepath.Join(paths.ConfigDir, "demo.env")
	assert.NoError(t, os.WriteFile(envFile, []byte("WORKLOAD_TYPE=tofu\nWORKLOAD_TOKEN=envtoken\n"), 0644))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)

	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, nil, paths)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer envtoken")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestApproveServerUsesLockedTokenOverEnv(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "plan.tfplan"), []byte("plan"), 0600))

	envFile := filepath.Join(paths.ConfigDir, "demo.env")
	assert.NoError(t, os.WriteFile(envFile, []byte("WORKLOAD_TYPE=tofu\nWORKLOAD_TOKEN=envtoken\n"), 0644))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer envtoken")

	cfg := reconciler.Config{WorkloadToken: "locked"}
	locks := reconciler.ConfigLocks{WorkloadToken: true}
	h := newServerHandler(cfg, locks, nil, paths)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer locked")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServerSyncWorkloadsPreservesInFlightRun(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	writeWorkloadEnv(t, paths, "demo", "WORKLOAD_TYPE=tofu\nRECONCILE_INTERVAL=10m\n")

	runner := newFakeRunner(true)
	dispatcher := newDispatcher(runner, context.Background())
	t.Cleanup(dispatcher.Stop)

	sched := scheduler.New(newTriggerRunner(dispatcher), nil, scheduler.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)
	t.Cleanup(sched.Stop)

	specs, err := loadValidWorkloads(10*time.Minute, reconciler.Config{}, reconciler.ConfigLocks{}, paths)
	assert.NoError(t, err)
	dispatcher.DisableExcept(workloadsSet(specs))
	sched.UpdateSpecs(toSchedulerSpecs(specs))

	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected runner to start")
	}

	// Add a second workload to simulate a rescan
	workdir2 := filepath.Join(paths.RuntimeDir, "demo2")
	assert.NoError(t, os.MkdirAll(workdir2, 0755))
	writeWorkloadEnv(t, paths, "demo2", "WORKLOAD_TYPE=tofu\nRECONCILE_INTERVAL=10m\n")

	specs2, err := loadValidWorkloads(10*time.Minute, reconciler.Config{}, reconciler.ConfigLocks{}, paths)
	assert.NoError(t, err)
	dispatcher.DisableExcept(workloadsSet(specs2))
	sched.UpdateSpecs(toSchedulerSpecs(specs2))

	// Unblock and verify demo finishes cleanly
	close(runner.block)
	assert.True(t, dispatcher.Wait("demo", 2*time.Second))
}

func TestServerReloadEndpoint(t *testing.T) {
	paths := makePaths(t)

	workdir := filepath.Join(paths.RuntimeDir, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	writeWorkloadEnv(t, paths, "demo", "WORKLOAD_TYPE=tofu\nRECONCILE_INTERVAL=10m\n")

	runner := newFakeRunner(false)
	dispatcher := newDispatcher(runner, context.Background())
	t.Cleanup(dispatcher.Stop)

	cfg := reconciler.Config{WorkloadToken: "secret"}
	h := newServerHandler(cfg, reconciler.ConfigLocks{}, dispatcher, paths).(*serverHandler)

	// Method not allowed (GET)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/reload", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	// Unauthorized (missing token)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/reload", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// 501 when syncWorkloads is nil
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/reload", nil)
	req.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)

	// Configure syncWorkloads
	h.syncWorkloads = func() error {
		specs, err := loadValidWorkloads(10*time.Minute, cfg, reconciler.ConfigLocks{}, paths)
		if err != nil {
			return err
		}
		dispatcher.DisableExcept(workloadsSet(specs))
		return nil
	}

	// Successful reload via /reload
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/reload", nil)
	req.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"ok"`)
	assert.Contains(t, rec.Body.String(), `"demo"`)

	// Successful reload via /rescan
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/rescan", nil)
	req.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"ok"`)
}

func TestServerReconcileAutoDiscovery(t *testing.T) {
	paths := makePaths(t)

	workdir1 := filepath.Join(paths.RuntimeDir, "demo1")
	assert.NoError(t, os.MkdirAll(workdir1, 0755))
	writeWorkloadEnv(t, paths, "demo1", "WORKLOAD_TYPE=tofu\nRECONCILE_INTERVAL=10m\n")

	runner := newFakeRunner(false)
	dispatcher := newDispatcher(runner, context.Background())
	t.Cleanup(dispatcher.Stop)

	cfg := reconciler.Config{}
	h := newServerHandler(cfg, reconciler.ConfigLocks{}, dispatcher, paths).(*serverHandler)
	h.syncWorkloads = func() error {
		specs, err := loadValidWorkloads(10*time.Minute, cfg, reconciler.ConfigLocks{}, paths)
		if err != nil {
			return err
		}
		dispatcher.DisableExcept(workloadsSet(specs))
		return nil
	}

	// Initial sync enables demo1
	assert.NoError(t, h.syncWorkloads())
	assert.Equal(t, []string{"demo1"}, dispatcher.EnabledWorkloads())

	// Create demo2 on disk without manually calling syncWorkloads
	workdir2 := filepath.Join(paths.RuntimeDir, "demo2")
	assert.NoError(t, os.MkdirAll(workdir2, 0755))
	writeWorkloadEnv(t, paths, "demo2", "WORKLOAD_TYPE=tofu\nRECONCILE_INTERVAL=10m\n")

	// Triggering demo2 automatically discovers it and succeeds with 202 Accepted
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/reconcile/demo2", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.True(t, dispatcher.Wait("demo2", 2*time.Second))

	// Verify demo2 is now in EnabledWorkloads
	assert.Equal(t, []string{"demo1", "demo2"}, dispatcher.EnabledWorkloads())

	// Unknown workload returns 404 Not Found (runtime directory missing)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/reconcile/unknown", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Disabled workload (dir exists but RECONCILE_ENABLED=false) returns 409 Conflict
	workdirDisabled := filepath.Join(paths.RuntimeDir, "disabled-wkld")
	assert.NoError(t, os.MkdirAll(workdirDisabled, 0755))
	writeWorkloadEnv(t, paths, "disabled-wkld", "WORKLOAD_TYPE=tofu\nRECONCILE_ENABLED=false\nRECONCILE_INTERVAL=10m\n")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/reconcile/disabled-wkld", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestServerMethodNotAllowedReturnsAllowHeader(t *testing.T) {
	paths := makePaths(t)
	runner := newFakeRunner(false)
	dispatcher := newDispatcher(runner, context.Background())
	t.Cleanup(dispatcher.Stop)

	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, dispatcher, paths)

	// GET /reload
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/reload", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "POST", rec.Header().Get("Allow"))

	// GET /reconcile/demo
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/reconcile/demo", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "POST", rec.Header().Get("Allow"))

	// GET /approve/demo
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/approve/demo", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "POST", rec.Header().Get("Allow"))
}

func TestServerManualWorkloadZeroIntervalCanBeReconciled(t *testing.T) {
	paths := makePaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "manual-only")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	// RECONCILE_INTERVAL=0 means no periodic scheduling, but workload is enabled
	writeWorkloadEnv(t, paths, "manual-only", "WORKLOAD_TYPE=tofu\nRECONCILE_INTERVAL=0\n")

	runner := newFakeRunner(false)
	dispatcher := newDispatcher(runner, context.Background())
	t.Cleanup(dispatcher.Stop)

	cfg := reconciler.Config{}
	specs, err := loadValidWorkloads(0, cfg, reconciler.ConfigLocks{}, paths)
	assert.NoError(t, err)

	// Verify workload is in valid specs and dispatcher enabled set
	dispatcher.DisableExcept(workloadsSet(specs))
	assert.Equal(t, []string{"manual-only"}, dispatcher.EnabledWorkloads())

	// Verify scheduler specs excludes interval <= 0
	schedSpecs := toSchedulerSpecs(specs)
	assert.Empty(t, schedSpecs)

	h := newServerHandler(cfg, reconciler.ConfigLocks{}, dispatcher, paths)

	// Triggering manual-only workload via API succeeds
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/reconcile/manual-only", nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.True(t, dispatcher.Wait("manual-only", 2*time.Second))
	assert.Equal(t, "manual-only", runner.workload)
}
