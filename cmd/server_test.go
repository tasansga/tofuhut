package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"tofuhut/internal/reconciler"
)

type fakeRunner struct {
	started  chan struct{}
	block    chan struct{}
	workload string
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
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	workdir := filepath.Join(base, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer token")

	h := newServerHandler(reconciler.Config{WorkloadToken: "token"}, reconciler.ConfigLocks{}, nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestReconcileStartsWorkload(t *testing.T) {
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	workdir := filepath.Join(base, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))

	runner := newFakeRunner(false)
	dispatcher := newDispatcher(runner, context.Background())
	t.Cleanup(dispatcher.Stop)
	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, dispatcher)

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
}

func TestReconcileRejectsUnauthorized(t *testing.T) {
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	workdir := filepath.Join(base, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))

	runner := newFakeRunner(false)
	dispatcher := newDispatcher(runner, context.Background())
	t.Cleanup(dispatcher.Stop)
	h := newServerHandler(reconciler.Config{WorkloadToken: "token"}, reconciler.ConfigLocks{}, dispatcher)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/reconcile/demo", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestReconcileReturnsLockedWhenRunning(t *testing.T) {
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	workdir := filepath.Join(base, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))

	runner := newFakeRunner(true)
	dispatcher := newDispatcher(runner, context.Background())
	t.Cleanup(dispatcher.Stop)
	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, dispatcher)

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
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	workdir := filepath.Join(base, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "plan.tfplan"), []byte("plan"), 0600))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer token")

	h := newServerHandler(reconciler.Config{WorkloadToken: "token"}, reconciler.ConfigLocks{}, nil)
	h.ServeHTTP(rec, req)

	approvePath := filepath.Join(workdir, "approve")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.FileExists(t, approvePath)
}

func TestApproveServerUnauthorized(t *testing.T) {
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	workdir := filepath.Join(base, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "plan.tfplan"), []byte("plan"), 0600))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)

	h := newServerHandler(reconciler.Config{WorkloadToken: "token"}, reconciler.ConfigLocks{}, nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestApproveServerAllowsWithoutToken(t *testing.T) {
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	workdir := filepath.Join(base, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "plan.tfplan"), []byte("plan"), 0600))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)

	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestApproveServerRejectsInvalidWorkload(t *testing.T) {
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/../bad", nil)

	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestApproveServerRejectsDotWorkloads(t *testing.T) {
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/..", nil)

	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestApproveServerUsesTokenFromEnvFile(t *testing.T) {
	base := t.TempDir()
	restoreWorkDir := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restoreWorkDir)
	envBase := t.TempDir()
	restoreEnvDir := reconciler.SetEnvDirForTests(envBase)
	t.Cleanup(restoreEnvDir)

	workdir := filepath.Join(base, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "plan.tfplan"), []byte("plan"), 0600))

	envFile := filepath.Join(envBase, "demo.env")
	assert.NoError(t, os.WriteFile(envFile, []byte("WORKLOAD_TOKEN=envtoken\n"), 0644))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)

	h := newServerHandler(reconciler.Config{}, reconciler.ConfigLocks{}, nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer envtoken")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestApproveServerUsesLockedTokenOverEnv(t *testing.T) {
	base := t.TempDir()
	restoreWorkDir := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restoreWorkDir)
	envBase := t.TempDir()
	restoreEnvDir := reconciler.SetEnvDirForTests(envBase)
	t.Cleanup(restoreEnvDir)

	workdir := filepath.Join(base, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "plan.tfplan"), []byte("plan"), 0600))

	envFile := filepath.Join(envBase, "demo.env")
	assert.NoError(t, os.WriteFile(envFile, []byte("WORKLOAD_TOKEN=envtoken\n"), 0644))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer envtoken")

	cfg := reconciler.Config{WorkloadToken: "locked"}
	locks := reconciler.ConfigLocks{WorkloadToken: true}
	h := newServerHandler(cfg, locks, nil)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer locked")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}
