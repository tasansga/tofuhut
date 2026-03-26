package reconciler

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func setupFakeTofu(t *testing.T, dir string, runtimeDir string, script string) {
	t.Helper()
	path := filepath.Join(dir, "tofu")
	setupFakeTool(t, path, runtimeDir, script)
}

func setupFakeTool(t *testing.T, path string, runtimeDir string, script string) {
	t.Helper()
	if strings.Contains(script, "__TOFUHUT_PLAN_FILE__") {
		planPath := filepath.Join(runtimeDir, "workload", "plan.tfplan")
		script = strings.ReplaceAll(script, "__TOFUHUT_PLAN_FILE__", planPath)
	}
	err := os.WriteFile(path, []byte(script), 0755)
	assert.NoError(t, err)

	dir := filepath.Dir(path)
	oldPath := os.Getenv("PATH")
	assert.NoError(t, os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath))
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})
}

func newWorkloadDir(t *testing.T, base string) string {
	t.Helper()
	err := os.MkdirAll(base, 0755)
	assert.NoError(t, err)
	return base
}

func withTempPaths(t *testing.T) Paths {
	t.Helper()
	return Paths{
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
}

func TestRunPlanOnlyNoChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	planPath := filepath.Join(workdir, "plan.tfplan")

	tmpBin := t.TempDir()
	setupFakeTofu(t, tmpBin, paths.RuntimeDir, "#!/bin/sh\nif [ \"$1\" = \"init\" ]; then exit 0; fi\nif [ \"$1\" = \"plan\" ]; then exit 0; fi\nexit 0\n")

	cfg := Config{WorkloadType: "tofu", Mode: "plan"}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	assert.NoFileExists(t, planPath)
}

func TestRunApplyWritesPlanAndWaitsForApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	planPath := filepath.Join(workdir, "plan.tfplan")
	planTextPath := filepath.Join(workdir, "workload-plan.txt")

	tmpBin := t.TempDir()
	setupFakeTofu(t, tmpBin, paths.RuntimeDir, "#!/bin/sh\nif [ \"$1\" = \"init\" ]; then exit 0; fi\nif [ \"$1\" = \"plan\" ]; then echo \"planned\"; touch \"__TOFUHUT_PLAN_FILE__\"; exit 2; fi\nexit 0\n")

	cfg := Config{WorkloadType: "tofu", Mode: "apply", WorkloadToken: "token"}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	assert.FileExists(t, planTextPath)
	assert.FileExists(t, planPath)
	assertFileMode(t, planTextPath, 0600)
	assertFileMode(t, planPath, 0600)
}

func TestRunApplyUsesApprovedPlan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	planPath := filepath.Join(workdir, "plan.tfplan")
	planTextPath := filepath.Join(workdir, "workload-plan.txt")
	approvePath := filepath.Join(workdir, "approve")
	applyLog := filepath.Join(workdir, "apply.log")

	assert.NoError(t, os.WriteFile(planPath, []byte("binary"), 0644))
	assert.NoError(t, os.WriteFile(planTextPath, []byte("text"), 0644))
	assert.NoError(t, os.WriteFile(approvePath, []byte("ok"), 0644))

	tmpBin := t.TempDir()
	setupFakeTofu(t, tmpBin, paths.RuntimeDir, "#!/bin/sh\nif [ \"$1\" = \"init\" ]; then exit 0; fi\nif [ \"$1\" = \"apply\" ]; then echo \"apply $@\" >> \""+applyLog+"\"; exit 0; fi\nexit 0\n")

	cfg := Config{WorkloadType: "tofu", Mode: "apply", WorkloadToken: "token"}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)

	assert.NoFileExists(t, planPath)
	assert.NoFileExists(t, planTextPath)
	assert.NoFileExists(t, approvePath)

	data, err := os.ReadFile(applyLog)
	assert.NoError(t, err)
	assert.True(t, strings.Contains(string(data), "apply -input=false -auto-approve "+planPath))
}

func TestRunApplyStaleApprovalRemoved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	approvePath := filepath.Join(workdir, "approve")
	assert.NoError(t, os.WriteFile(approvePath, []byte("ok"), 0644))

	tmpBin := t.TempDir()
	setupFakeTofu(t, tmpBin, paths.RuntimeDir, "#!/bin/sh\nif [ \"$1\" = \"init\" ]; then exit 0; fi\nif [ \"$1\" = \"plan\" ]; then exit 0; fi\nexit 0\n")

	cfg := Config{WorkloadType: "tofu", Mode: "apply", WorkloadToken: "token"}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	assert.NoFileExists(t, approvePath)
}

func TestRunAutoApplyAppliesImmediately(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	applyLog := filepath.Join(workdir, "apply.log")

	tmpBin := t.TempDir()
	setupFakeTofu(t, tmpBin, paths.RuntimeDir, "#!/bin/sh\nif [ \"$1\" = \"init\" ]; then exit 0; fi\nif [ \"$1\" = \"plan\" ]; then exit 2; fi\nif [ \"$1\" = \"apply\" ]; then echo \"apply $@\" >> \""+applyLog+"\"; exit 0; fi\nexit 0\n")

	cfg := Config{WorkloadType: "tofu", Mode: "auto-apply"}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)

	data, err := os.ReadFile(applyLog)
	assert.NoError(t, err)
	assert.True(t, strings.Contains(string(data), "apply -input=false -auto-approve"))
}

func TestRunApplyNoChangesCleansPlanArtifacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	planPath := filepath.Join(workdir, "plan.tfplan")
	planTextPath := filepath.Join(workdir, "workload-plan.txt")

	tmpBin := t.TempDir()
	setupFakeTofu(t, tmpBin, paths.RuntimeDir, "#!/bin/sh\nif [ \"$1\" = \"init\" ]; then exit 0; fi\nif [ \"$1\" = \"plan\" ]; then touch \"__TOFUHUT_PLAN_FILE__\"; exit 0; fi\nexit 0\n")

	cfg := Config{WorkloadType: "tofu", Mode: "apply", WorkloadToken: "token"}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	assert.NoFileExists(t, planPath)
	assert.NoFileExists(t, planTextPath)
}

func TestRunAnsiblePlanUsesCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	playbookPath := filepath.Join(workdir, "playbook.yml")
	assert.NoError(t, os.WriteFile(playbookPath, []byte("ok"), 0644))

	tmpBin := t.TempDir()
	logPath := filepath.Join(workdir, "ansible.log")
	setupFakeTool(t, filepath.Join(tmpBin, "ansible-playbook"), paths.RuntimeDir, "#!/bin/sh\necho \"$@\" > \""+logPath+"\"\nexit 0\n")

	cfg := Config{WorkloadType: "ansible", Mode: "plan"}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)

	data, err := os.ReadFile(logPath)
	assert.NoError(t, err)
	assert.True(t, strings.Contains(string(data), "--check"))
}

func TestRunAnsibleApplyRequiresApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	playbookPath := filepath.Join(workdir, "playbook.yml")
	assert.NoError(t, os.WriteFile(playbookPath, []byte("ok"), 0644))

	tmpBin := t.TempDir()
	logPath := filepath.Join(workdir, "ansible.log")
	setupFakeTool(t, filepath.Join(tmpBin, "ansible-playbook"), paths.RuntimeDir, "#!/bin/sh\necho \"$@\" > \""+logPath+"\"\nexit 0\n")

	cfg := Config{WorkloadType: "ansible", Mode: "apply", WorkloadToken: "token"}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	assert.NoFileExists(t, logPath)
}

func TestRunAnsibleChangedOnlySkipsWhenUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	playbookPath := filepath.Join(workdir, "playbook.yml")
	assert.NoError(t, os.WriteFile(playbookPath, []byte("ok"), 0644))

	tmpBin := t.TempDir()
	logPath := filepath.Join(workdir, "ansible.log")
	setupFakeTool(t, filepath.Join(tmpBin, "ansible-playbook"), paths.RuntimeDir, "#!/bin/sh\necho \"run\" >> \""+logPath+"\"\nexit 0\n")

	cfg := Config{WorkloadType: "ansible", Mode: "plan", ReconcileChangedOnly: true}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	err = Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)

	data, err := os.ReadFile(logPath)
	assert.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 1)

	assert.NoError(t, os.WriteFile(playbookPath, []byte("changed"), 0644))
	err = Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)

	data, err = os.ReadFile(logPath)
	assert.NoError(t, err)
	lines = strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 2)
}

func TestRunAnsibleChangedOnlyForceContextBypassesSkip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	playbookPath := filepath.Join(workdir, "playbook.yml")
	assert.NoError(t, os.WriteFile(playbookPath, []byte("ok"), 0644))

	tmpBin := t.TempDir()
	logPath := filepath.Join(workdir, "ansible.log")
	setupFakeTool(t, filepath.Join(tmpBin, "ansible-playbook"), paths.RuntimeDir, "#!/bin/sh\necho \"run\" >> \""+logPath+"\"\nexit 0\n")

	cfg := Config{WorkloadType: "ansible", Mode: "plan", ReconcileChangedOnly: true}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	err = Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)

	forceCtx := WithForceReconcile(context.Background(), true)
	err = RunWithContext(forceCtx, "workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)

	data, err := os.ReadFile(logPath)
	assert.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 2)
}

func TestRunAnsibleChangedOnlyApplyUsesApprovalDespiteUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	playbookPath := filepath.Join(workdir, "playbook.yml")
	assert.NoError(t, os.WriteFile(playbookPath, []byte("ok"), 0644))
	approvePath := filepath.Join(workdir, "approve")

	tmpBin := t.TempDir()
	logPath := filepath.Join(workdir, "ansible.log")
	setupFakeTool(t, filepath.Join(tmpBin, "ansible-playbook"), paths.RuntimeDir, "#!/bin/sh\necho \"run\" >> \""+logPath+"\"\nexit 0\n")

	cfg := Config{WorkloadType: "ansible", Mode: "apply", WorkloadToken: "token", ReconcileChangedOnly: true}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	assert.NoFileExists(t, logPath)

	assert.NoError(t, os.WriteFile(approvePath, []byte("ok"), 0600))
	err = Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)

	data, err := os.ReadFile(logPath)
	assert.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 1)
}

func TestRunDNSControlPlanNoChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "dnsconfig.js"), []byte("ok"), 0644))
	previewTextPath := filepath.Join(workdir, "workload-preview.txt")
	previewReportPath := filepath.Join(workdir, "preview-report.json")

	tmpBin := t.TempDir()
	setupFakeTool(t, filepath.Join(tmpBin, "dnscontrol"), paths.RuntimeDir, "#!/bin/sh\ncmd=\"$1\"\nshift\nif [ \"$cmd\" = \"preview\" ]; then\nreport=\"\"\nwhile [ $# -gt 0 ]; do\nif [ \"$1\" = \"--report\" ]; then\nshift\nreport=\"$1\"\nbreak\nfi\nshift\ndone\necho \"dns preview\"\necho '[{\"domain\":\"example.com\",\"corrections\":0}]' > \"$report\"\nexit 0\nfi\nexit 1\n")

	cfg := Config{WorkloadType: "dnscontrol", Mode: "plan"}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	assert.NoFileExists(t, previewTextPath)
	assert.FileExists(t, previewReportPath)
}

func TestRunDNSControlApplyWritesPreviewAndWaitsForApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "dnsconfig.js"), []byte("ok"), 0644))
	previewTextPath := filepath.Join(workdir, "workload-preview.txt")
	previewReportPath := filepath.Join(workdir, "preview-report.json")
	approvePendingPath := filepath.Join(workdir, "approve.pending")

	tmpBin := t.TempDir()
	setupFakeTool(t, filepath.Join(tmpBin, "dnscontrol"), paths.RuntimeDir, "#!/bin/sh\ncmd=\"$1\"\nshift\nif [ \"$cmd\" = \"preview\" ]; then\nreport=\"\"\nwhile [ $# -gt 0 ]; do\nif [ \"$1\" = \"--report\" ]; then\nshift\nreport=\"$1\"\nbreak\nfi\nshift\ndone\necho \"dns changes\"\necho '[{\"domain\":\"example.com\",\"corrections\":2}]' > \"$report\"\nexit 0\nfi\nexit 1\n")

	cfg := Config{WorkloadType: "dnscontrol", Mode: "apply", WorkloadToken: "token"}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	assert.FileExists(t, previewTextPath)
	assert.FileExists(t, previewReportPath)
	assert.FileExists(t, approvePendingPath)
	assertFileMode(t, previewTextPath, 0600)
	assertFileMode(t, previewReportPath, 0600)
	assertFileMode(t, approvePendingPath, 0600)
}

func TestRunDNSControlApplyUsesApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	assert.NoError(t, os.WriteFile(filepath.Join(workdir, "dnsconfig.js"), []byte("ok"), 0644))
	previewTextPath := filepath.Join(workdir, "workload-preview.txt")
	previewReportPath := filepath.Join(workdir, "preview-report.json")
	approvePendingPath := filepath.Join(workdir, "approve.pending")
	approvePath := filepath.Join(workdir, "approve")
	pushLog := filepath.Join(workdir, "dnscontrol.log")

	tmpBin := t.TempDir()
	setupFakeTool(t, filepath.Join(tmpBin, "dnscontrol"), paths.RuntimeDir, "#!/bin/sh\ncmd=\"$1\"\nshift\nif [ \"$cmd\" = \"preview\" ]; then\nreport=\"\"\nwhile [ $# -gt 0 ]; do\nif [ \"$1\" = \"--report\" ]; then\nshift\nreport=\"$1\"\nbreak\nfi\nshift\ndone\necho \"dns changes\"\necho '[{\"domain\":\"example.com\",\"corrections\":1}]' > \"$report\"\nexit 0\nfi\nif [ \"$cmd\" = \"push\" ]; then\necho \"push $@\" >> \""+pushLog+"\"\nexit 0\nfi\nexit 1\n")

	cfg := Config{WorkloadType: "dnscontrol", Mode: "apply", WorkloadToken: "token"}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	assert.FileExists(t, approvePendingPath)

	assert.NoError(t, os.WriteFile(approvePath, []byte("ok"), 0600))

	err = Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	assert.NoFileExists(t, approvePath)
	assert.NoFileExists(t, approvePendingPath)
	assert.NoFileExists(t, previewTextPath)
	assert.NoFileExists(t, previewReportPath)

	data, err := os.ReadFile(pushLog)
	assert.NoError(t, err)
	assert.True(t, strings.Contains(string(data), "push"))
}

func TestRunDNSControlChangedOnlySkipsWhenUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	paths := withTempPaths(t)
	workdir := filepath.Join(paths.RuntimeDir, "workload")
	newWorkloadDir(t, workdir)
	dnsConfigPath := filepath.Join(workdir, "dnsconfig.js")
	assert.NoError(t, os.WriteFile(dnsConfigPath, []byte("ok"), 0644))

	tmpBin := t.TempDir()
	logPath := filepath.Join(workdir, "dnscontrol.log")
	setupFakeTool(t, filepath.Join(tmpBin, "dnscontrol"), paths.RuntimeDir, "#!/bin/sh\ncmd=\"$1\"\nshift\nif [ \"$cmd\" = \"preview\" ]; then\necho \"preview\" >> \""+logPath+"\"\nreport=\"\"\nwhile [ $# -gt 0 ]; do\nif [ \"$1\" = \"--report\" ]; then\nshift\nreport=\"$1\"\nbreak\nfi\nshift\ndone\necho '[{\"domain\":\"example.com\",\"corrections\":0}]' > \"$report\"\nexit 0\nfi\nexit 1\n")

	cfg := Config{WorkloadType: "dnscontrol", Mode: "plan", ReconcileChangedOnly: true}
	err := Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)
	err = Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)

	data, err := os.ReadFile(logPath)
	assert.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 1)

	assert.NoError(t, os.WriteFile(dnsConfigPath, []byte("changed"), 0644))
	err = Run("workload", cfg, filepath.Join(t.TempDir(), "workload.env"), map[string]string{}, paths)
	assert.NoError(t, err)

	data, err = os.ReadFile(logPath)
	assert.NoError(t, err)
	lines = strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 2)
}

func TestRunCommandEmptyEnvPreserved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not supported on windows")
	}

	tmpDir := t.TempDir()
	envLog := filepath.Join(tmpDir, "env.log")

	setupFakeTofu(t, tmpDir, tmpDir, "#!/bin/sh\nenv > \""+envLog+"\"\nexit 0\n")

	_, err := runCommand(context.Background(), commandOptions{Env: []string{}}, "tofu")
	assert.NoError(t, err)

	data, err := os.ReadFile(envLog)
	assert.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// Some shells inject PWD/SHLVL/_ even with an empty environment.
	for _, line := range lines {
		if line == "" {
			continue
		}
		assert.True(t,
			strings.HasPrefix(line, "PWD=") ||
				strings.HasPrefix(line, "SHLVL=") ||
				strings.HasPrefix(line, "_="),
			"unexpected env line: %s",
			line,
		)
	}
}

func assertFileMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	assert.NoError(t, err)
	assert.Equal(t, expected, info.Mode().Perm())
}
