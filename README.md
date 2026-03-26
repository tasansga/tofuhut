# Tofuhut

Tofuhut is an infrastructure reconciler CLI for three workload types:

- `ansible`
- `dnscontrol`
- `tofu` (OpenTofu)

It supports `plan`/`apply`/`auto-apply` modes, approval-gated runs, optional `ntfy` notifications, and optional Gatus reporting.

## Usage

Run a workload once:

- `tofuhut workload run <name>`

Run approval/reconciliation server:

- `tofuhut server run --listen :8080`

Set logging at runtime:

- `tofuhut --log-level debug workload run <name>`
- `tofuhut --log-format json server run`

## Workload Layout

- Working directory: `/var/lib/tofuhut/workloads/<workload>`
- Env file: `/etc/tofuhut/workloads/<workload>.env`

Directories can be overridden via flags or env vars. Defaults:

- `workload config dir`: `/etc/tofuhut/workloads`
- `workload runtime dir`: `/var/lib/tofuhut/workloads`

Override using:

- `TOFUHUT_WORKLOAD_CONFIG_DIR`
- `TOFUHUT_WORKLOAD_RUNTIME_DIR`

Required workload files:

- `ansible`: `playbook.yml`
- `dnscontrol`: `dnsconfig.js`
- `tofu`: OpenTofu files like `*.tf`

## Environment Variables

Common:

- `WORKLOAD_TYPE` (required): `ansible`, `dnscontrol`, or `tofu`
- `MODE`: `plan`, `apply`, or `auto-apply` (default: `plan`)
- `GATUS_CLI_URL`, `GATUS_CLI_TOKEN`
- `NTFY_URL`, `NTFY_TOPIC`, `NTFY_TOKEN`
- `APPROVE_URL`, `WORKLOAD_TOKEN`
- `RECONCILE_ENABLED`, `RECONCILE_INTERVAL` (scheduler)
- `RECONCILE_CHANGED_ONLY` (default `false`; only for `ansible`/`dnscontrol`, reconcile only when watched file content changed)
- `TOFUHUT_WORKLOAD_CONFIG_DIR` (default `/etc/tofuhut/workloads`)
- `TOFUHUT_WORKLOAD_RUNTIME_DIR` (default `/var/lib/tofuhut/workloads`)
- `LOG_LEVEL` (`debug`, `info`, `warn`, `error`, `fatal`, `panic`; default `info`)
- `LOG_FORMAT` (`text` or `json`; default `text`)

Tofu-specific:

- `UPGRADE=true` -> adds `-upgrade` to `tofu init`
- `RECONFIGURE=true` -> adds `-reconfigure` to `tofu init`

## Workload Behavior

| Workload type | `plan` | `apply` | `auto-apply` |
| --- | --- | --- | --- |
| `ansible` | `ansible-playbook -v -c local --check playbook.yml` | Waits for approval, then runs `ansible-playbook -v -c local playbook.yml` | Runs `ansible-playbook -v -c local playbook.yml` |
| `dnscontrol` | `dnscontrol preview --report preview-report.json` | Writes preview artifacts, waits for approval, then runs `dnscontrol push` | Runs `dnscontrol push` when preview shows changes |
| `tofu` | `tofu init`, then `tofu plan -input=false -no-color -detailed-exitcode` | Stores plan artifacts, waits for approval, then applies stored plan | Runs `tofu apply -input=false -auto-approve` when plan shows changes |

## Approval and API

In `MODE=apply`, approval is recorded by creating:

- `/var/lib/tofuhut/workloads/<workload>/approve`

HTTP endpoints:

- `POST /approve/<workload>` records approval
- `POST /reconcile/<workload>` triggers reconciliation

If `WORKLOAD_TOKEN` is set, both endpoints require:

- `Authorization: Bearer <token>`

## Approval Artifacts by Workload Type

- `ansible`: creates `approve.pending` while waiting for approval
- `dnscontrol`: writes `<workload>-preview.txt` and `preview-report.json`, and creates `approve.pending`
- `tofu`: writes `plan.tfplan` and `<workload>-plan.txt`

On successful approved run, Tofuhut removes approval/pending artifacts.

## Notifications and Reporting

- `ntfy` notifications are sent when approval is required and `NTFY_URL` + `NTFY_TOPIC` are set
- ntfy Approve action is included when `APPROVE_URL` is set
- Gatus success/failure reporting is enabled when `GATUS_CLI_URL` + token are configured
- Server responses include CORS header `Access-Control-Allow-Origin: *`

## Environment Propagation

Tofuhut passes a restricted allowlist of host env vars to workload commands, then merges values from the workload env file. Add provider credentials explicitly in each workload env file.

## Systemd Templates

Embedded example workload templates, available with `tofuhut workload print`:

- `tofuhut-workload@.service`
- `tofuhut-workload@.timer`

Workload service command:

- `ExecStart=tofuhut workload run %i`
