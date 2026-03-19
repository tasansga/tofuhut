# Examples

This directory contains minimal workload examples for all supported workload types:

- `ansible`
- `dnscontrol`
- `tofu`

## Layout

- `examples/ansible/playbook.yml`
- `examples/ansible/demo-ansible.env`
- `examples/dnscontrol/dnsconfig.js`
- `examples/dnscontrol/demo-dnscontrol.env`
- `examples/tofu/main.tf`
- `examples/tofu/demo-tofu.env`

## Using An Example

Pick a workload name, for example `demo-tofu`:

1. Create the runtime workload directory:
   - `sudo mkdir -p /var/lib/tofuhut/workloads/demo-tofu`
2. Copy workload files from an example:
   - `sudo cp examples/tofu/main.tf /var/lib/tofuhut/workloads/demo-tofu/`
3. Copy and adjust env file:
   - `sudo cp examples/tofu/demo-tofu.env /etc/tofuhut/workloads/`
4. Run once:
   - `tofuhut workload run demo-tofu`

Use the matching example directory for `ansible` and `dnscontrol`.
