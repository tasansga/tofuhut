package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"tofuhut/internal/reconciler"
)

func TestApproveServerRejectsMissingPlan(t *testing.T) {
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	workdir := filepath.Join(base, "demo")
	assert.NoError(t, os.MkdirAll(workdir, 0755))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer token")

	h := newApproveHandler(reconciler.Config{ApproveToken: "token"}, reconciler.ConfigLocks{})
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
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

	h := newApproveHandler(reconciler.Config{ApproveToken: "token"}, reconciler.ConfigLocks{})
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

	h := newApproveHandler(reconciler.Config{ApproveToken: "token"}, reconciler.ConfigLocks{})
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

	h := newApproveHandler(reconciler.Config{}, reconciler.ConfigLocks{})
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestApproveServerRejectsInvalidWorkload(t *testing.T) {
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/../bad", nil)

	h := newApproveHandler(reconciler.Config{}, reconciler.ConfigLocks{})
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestApproveServerRejectsDotWorkloads(t *testing.T) {
	base := t.TempDir()
	restore := reconciler.SetWorkDirBaseForTests(base)
	t.Cleanup(restore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/..", nil)

	h := newApproveHandler(reconciler.Config{}, reconciler.ConfigLocks{})
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
	assert.NoError(t, os.WriteFile(envFile, []byte("APPROVE_TOKEN=envtoken\n"), 0644))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)

	h := newApproveHandler(reconciler.Config{}, reconciler.ConfigLocks{})
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
	assert.NoError(t, os.WriteFile(envFile, []byte("APPROVE_TOKEN=envtoken\n"), 0644))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer envtoken")

	cfg := reconciler.Config{ApproveToken: "locked"}
	locks := reconciler.ConfigLocks{ApproveToken: true}
	h := newApproveHandler(cfg, locks)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/approve/demo", nil)
	req.Header.Set("Authorization", "Bearer locked")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}
