# Tofuhut

Tofuhut is an infrastructure reconciler CLI for three workload types:

- `ansible`
- `dnscontrol`
- `tofu` (OpenTofu)

It supports `plan`/`apply`/`auto-apply` modes, approval-gated runs, periodic scheduling, optional ntfy notifications, optional Gatus reporting, and Prometheus-compatible metrics in server mode.

## Quick Start

Either run a workload once:

- `tofuhut workload run <name>`

Or run the approval/reconciliation server:

- `tofuhut server run --listen :8080`

Set logging at runtime:

- `tofuhut --log-level debug workload run <name>`
- `tofuhut --log-format json server run`

## Workload Layout

Default paths:

- Workload runtime dir: `/var/lib/tofuhut/workloads/<workload>`
- Workload env file: `/etc/tofuhut/workloads/<workload>.env`

Override root directories with:

- `TOFUHUT_WORKLOAD_CONFIG_DIR`
- `TOFUHUT_WORKLOAD_RUNTIME_DIR`

Required files per workload type:

- `ansible`: `playbook.yml`
- `dnscontrol`: `dnsconfig.js`
- `tofu`: OpenTofu files such as `*.tf`

## Runtime Requirements

Tofuhut executes workload tools directly. The required binary must be available in `PATH`:

- `ansible` workloads: `ansible-playbook` ([Ansible](https://www.ansible.com/))
- `dnscontrol` workloads: `dnscontrol` ([DNSControl](https://dnscontrol.org/))
- `tofu` workloads: `tofu` ([OpenTofu](https://opentofu.org/))

## Environment Variables

### Required

- `WORKLOAD_TYPE`: `ansible`, `dnscontrol`, or `tofu`

### Core execution

- `MODE`: `plan`, `apply`, or `auto-apply` (default: `plan`)

### Scheduler

- `RECONCILE_ENABLED` (default: `true`)
- `RECONCILE_INTERVAL` (default comes from server flag `--scheduler-default-interval`)
- `RECONCILE_CHANGED_ONLY` (default: `false`)
  - For `ansible`/`dnscontrol`: if `true`, Tofuhut hashes `playbook.yml`/`dnsconfig.js` and skips unchanged runs.
  - For `tofu`: currently ignored.
  - Manual `POST /reconcile/<workload>` still forces a run.

### Hooks

- `PRE_RECONCILE_HOOK`, `POST_RECONCILE_HOOK`: optional absolute script paths.
- `PRE_RECONCILE_TIMEOUT`, `POST_RECONCILE_TIMEOUT`: optional duration values (for example `30s`). Unset means no timeout.

Hook execution order and behavior:

- Pre-hook runs before every reconcile attempt.
- For changed-only workloads, pre-hook runs before file-change evaluation.
- Post-hook runs after every attempt, including changed-only skips.

Hook failure behavior:

- Pre-hook non-zero/exec failure fails the reconcile run.
- Post-hook non-zero/exec failure fails an otherwise successful reconcile run.
- If the main reconcile already failed, post-hook failure is logged as warning and the original reconcile error is preserved.

Hook env vars provided by Tofuhut:

- `TOFUHUT_WORKLOAD`
- `TOFUHUT_WORKDIR`
- `TOFUHUT_RESULT` (`success`, `error`, `canceled`)
- `TOFUHUT_REQUEST_ID` (if available)
- `TOFUHUT_TRIGGER` (`scheduler`, `api_manual`, `cli`, or `unknown`)

### Approval and API auth

- `WORKLOAD_TOKEN`: protects both `POST /approve/<workload>` and `POST /reconcile/<workload>` via `Authorization: Bearer <token>`
- `APPROVE_URL`: used to build ntfy approve action links/buttons

### Notifications and reporting

- `NTFY_URL`, `NTFY_TOPIC`, `NTFY_TOKEN`
  - Sends approval-required notifications when approval is needed.
  - Includes an approve action when `APPROVE_URL` is set.
- `GATUS_CLI_URL`, `GATUS_CLI_TOKEN`
  - Reports run success/failure to the Gatus HTTP API.
  - If no token is set, Tofuhut also supports token lookup via an optional `gatus_cli_token_for_name` shell function sourced from the workload env file.

### Logging and paths

- `LOG_LEVEL`: `debug`, `info`, `warn`, `error`, `fatal`, `panic` (default: `info`)
- `LOG_FORMAT`: `text` or `json` (default: `text`)
- `TOFUHUT_WORKLOAD_CONFIG_DIR`
- `TOFUHUT_WORKLOAD_RUNTIME_DIR`

### Tofu-only

- `UPGRADE=true` adds `-upgrade` to `tofu init`
- `RECONFIGURE=true` adds `-reconfigure` to `tofu init`

## Workload Behavior

| Workload type | `plan` | `apply` | `auto-apply` |
| --- | --- | --- | --- |
| `ansible` | `ansible-playbook -v -c local --check playbook.yml` | Waits for approval, then runs `ansible-playbook -v -c local playbook.yml` | Runs `ansible-playbook -v -c local playbook.yml` |
| `dnscontrol` | `dnscontrol preview --report preview-report.json` | Writes preview artifacts, waits for approval, then runs `dnscontrol push` | Runs `dnscontrol push` when preview shows changes |
| `tofu` | `tofu init`, then `tofu plan -input=false -no-color -detailed-exitcode` | Stores plan artifacts, waits for approval, then applies stored plan | Runs `tofu apply -input=false -auto-approve` when plan shows changes |

## API Endpoints

- `POST /approve/<workload>` records approval.
- `POST /reconcile/<workload>` queues reconciliation.

In `MODE=apply`, approval is represented by:

- `/var/lib/tofuhut/workloads/<workload>/approve`

If `WORKLOAD_TOKEN` is set, both API endpoints require:

- `Authorization: Bearer <token>`

## Approval Artifacts by Workload Type

- `ansible`: creates `approve.pending` while waiting for approval
- `dnscontrol`: writes `<workload>-preview.txt` and `preview-report.json`, and creates `approve.pending`
- `tofu`: writes `plan.tfplan` and `<workload>-plan.txt`

On successful approved run, Tofuhut removes approval/pending artifacts.

## Observability

- `GET /metrics` in `tofuhut server run` exposes Prometheus scrape output backed by OpenTelemetry metrics.
- API responses include CORS header `Access-Control-Allow-Origin: *`.

## Environment Propagation

Tofuhut passes a restricted allowlist of host environment variables to workload commands, then merges values from the workload env file. Add provider credentials explicitly in each workload env file.
