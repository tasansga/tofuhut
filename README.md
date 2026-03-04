**Tofuhut**
Tofuhut is a small OpenTofu reconciler CLI. It runs `tofu init`, `tofu plan` (with detailed exit codes), and optionally `tofu apply` for a workload directory, while supporting optional Gatus reporting.

**Usage**
Run a workload once: `tofuhut workload run <name>`
Print embedded templates:
- `tofuhut print systemd-service`
- `tofuhut print systemd-timer`
- `tofuhut print workload-env`

**Workload Layout**
- `/var/lib/tofuhut/workloads/<workload>` is the working directory.
- `/etc/tofuhut/workloads/<workload>.env` provides workload-specific environment variables.

**Environment Variables**
- `MODE=apply` to run `tofu apply` after a plan with changes. Default is plan only.
- `UPGRADE=true` adds `-upgrade` to `tofu init`.
- `RECONFIGURE=true` adds `-reconfigure` to `tofu init`.
- `GATUS_CLI_URL` and `GATUS_CLI_TOKEN` enable Gatus reporting. Alternatively define a function `gatus_cli_token_for_name` in the env file to supply a token for the workload name.

**Environment Propagation**
Tofuhut passes a restricted allowlist of host environment variables to `tofu`, then merges in variables from the workload env file. The allowlist is intentionally minimal (PATH, locale, proxy, certs, temp dirs, and basic user/home fields). Add provider credentials (e.g. AWS_) to the workload env file explicitly.

**Systemd Templates**
Tofuhut embeds systemd unit templates for instance-based workloads:
- `tofuhut-workload@.service`
- `tofuhut-workload@.timer`
These use the instance name (`%i`) for the workload name. The service runs:
- `ExecStart=/usr/local/bin/tofuhut workload run %i`

**Notes**
- `TF_IN_AUTOMATION` defaults to `1` and `TF_INPUT=0` to keep runs non-interactive.
- `tofu plan` runs with `-no-color` and `-detailed-exitcode` to detect changes.
