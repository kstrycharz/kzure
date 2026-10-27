# Kzure implementation status

Last updated: October 27, 2026. Release: `0.1.0-dev`.

**Development resumed at the user's request.** The platform and retained workloads were restored after their containers had been removed. IPv4 subnet enforcement passed 17 integration groups; optional loopback HTTPS passed seven checks; see [REMOTE_ACCESS_PROGRESS.md](REMOTE_ACCESS_PROGRESS.md) for actual evidence and remaining release gates.

Final resumed verification: 16 foundation, 28 browser-shell, 14 native-access/network, six desktop and 17 IPv4 policy groups passed, together with seven HTTPS/WSS checks, Go tests/vet and the portal build. Metadata restored into a scratch database with 83 resource records. This is a working single-host development milestone, with the wider master-plan gates still open.

## Delivered foundation

- React/TypeScript portal: sign-in, dashboard, tenant switcher, catalog, real create-resource wizard, resource details, logs, live metrics, lifecycle actions, group/tenant/subscription creation, operations, cost table/CSV, host capability view, and scoped local-user creation.
- Go API, durable PostgreSQL operation worker, periodic desired-state reconciliation, and a separate token-authenticated Docker agent.
- Actual CPU/RAM admission and runtime limits, transaction-serialized quota checks, idempotency with body mismatch detection, encrypted service passwords/keys, owner-gated secret reveal, and managed object ownership validation.
- Catalog providers: static web, initial approved container app template, PostgreSQL, Valkey, dedicated S3 storage, and six Linux workspace distributions.
- Custom platform, static-site, storage-account, and workspace Docker images; image identity lock; pinned upstream build bases; Compose bootstrap and private generated configuration.
- Allocation intervals, elapsed showback, retained-storage accounting, and real Docker logs/metrics.
- Automated unit and integration verification with private local test reports. See the verification report for actual run results; no unexecuted checks are represented as passed.
- Browser shell for all six Linux workspace distributions: non-root PTY, resize/Ctrl-C/Unicode, reconnect/fullscreen, explicit contributor grants, active-session termination, short-lived single-use tickets, audit metadata, output flow control, and local cleanup after agent failure. The 22-group terminal integration suite passed; see [REMOTE_ACCESS_PROGRESS.md](REMOTE_ACCESS_PROGRESS.md).
- Subsequent remote-access development: six SSH workspace variants, a restricted native bastion, public-key management, private subnets/IPAM, DNS/service connectors, Ubuntu desktop and browser RDP. The native/network suite passed 14 groups and the desktop protocol suite passed four groups; a real graphical desktop was also verified in the portal. Broader release gates and final regression remain open.

## Master-plan gate status

| Phase | Status | Remaining gate work |
|---|---|---|
| P0: host proof | Partial | Dedicated Linux host with quota-capable filesystem; local TLS/LAN design; GPU qualification. Docker lifecycle and S3/persistence proofs are exercised on Docker Desktop. |
| P1: bootstrap/design | Partial | API contract completeness, full design documentation and clean supported-Linux install qualification. |
| P2: identity/governance | Partial | OIDC/MFA, RLS using restricted roles, finer scoped permissions, session/account management, audit UI. Local sessions and API tenant boundaries work. |
| P3: resource engine | Partial | Hard storage quotas, broader failure/retry/fencing tests, resize and provider capability schemas. Basic durable lifecycle and periodic reconciliation work. |
| P4: static apps | Partial | Archive builds, revisions, atomic routing/rollback, custom domains and TLS. A single HTML/default site deploys to a real container. |
| P5: storage/MVP | Partial | Portal object browser, key rotation, enforced quota, backup/restore and clean-host release acceptance. Actual signed S3 operations are tested. |
| P6: PaaS/data | Partial | Source builds, app plans, runtime recipes, private service bindings, database backup/restore. Dedicated database/cache providers run. |
| P7: WAF/policy | Partial | IPv4 subnet policy, UI and 17 packet-test groups delivered. WAF provider, wider egress qualification and no-bypass ingress remain. |
| P8: Linux/GPU jobs | Partial | Browser shell is delivered for localhost development across all six distros. Home management, CPU job queue, GPU inventory and actual device qualification remain. Six Linux images are built and verified as non-root with writable /data. |
| P9: costing | Partial | Editable effective-dated rates, budgets, GPU meters, outage uncertainty, overhead pools. Fixed-rate numeric allocation accounting and CSV are present. |
| P10: release | Partial | Full recovery/upgrade drill, SBOM/vulnerability/signature checks, accessibility qualification, offline bundle and production hardening. Development image builds and localhost boot work. |
| R3: multiple hosts | Planned | Enrollment/mTLS, placement, data locality/migration, fencing and HA design. |

## Current execution priorities

The remote-access scope is specified in [REMOTE_ACCESS_NETWORKING_PLAN.md](REMOTE_ACCESS_NETWORKING_PLAN.md). Browser shell and development SSH/bastion/desktop/network foundations are implemented; enforced networking has unfinished source and remote-release gates remain open. Work is paused. Use the detailed progress log for the resume order rather than treating all phases as planned or complete. The foundation backlog below remains relevant, particularly identity, isolation, and recovery dependencies.

1. Keep the current Docker integration tests passing and qualify broader recovery/concurrency cases.
2. Add a managed image import path so container apps can deploy operator-approved application images.
3. Browser shell with separate authorization/audit is delivered for localhost development. Continue the remote-access track with SSH images, private networking, bastions and desktops, following its remaining gates.
4. Implement local HTTPS ingress, explicit private service connections, and WAF protection with no direct-origin bypass.
5. Implement restricted database roles/RLS and federated identity, then qualify tenant isolation at both API and network layers.
6. Implement the hard-quota storage adapter and backup/restore on the actual supported Linux deployment host.
7. Extend PaaS builds, jobs/GPU scheduling and cost controls to meet the remaining master-plan gates.

## Operator notes

All persistent configuration lives in the local workspace/owned Docker volumes. `.env` and `.local/` contain private installation material and are ignored by Git. The browser-facing development endpoint is `http://localhost:8088`. Demonstration resources created by the smoke test are intentionally left available for inspection. Isolated test resources may appear in historical activity/cost views after deletion, as required by allocation history.

