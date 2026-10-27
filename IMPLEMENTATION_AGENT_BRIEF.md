# Implementation agent brief — Kzure

This is the implementation handoff specification. A working 0.1.0 development foundation and Docker images now exist; consult `docs/implementation-status.md` before starting work. The next requested feature plan is `docs/REMOTE_ACCESS_NETWORKING_PLAN.md`, covering browser shells, SSH/RDP bastions, desktop images, and private networking. Its phased work extends the existing platform; do not rebuild the foundation or assume all master-plan gates have passed.

## Mission

Implement the platform described in `PROJECT_PLAN.md`, starting with one local Linux Docker host and an architecture that can expand to multiple hosts. The user confirmed **one host first, expand later**. Deliver real local resources behind a cloud-style web portal with tenants, subscriptions, resource groups, RBAC, quotas, and CPU/RAM/GPU/storage-based showback.

Read the full project plan and `cost-model.example.json` before modifying application code. Treat the plan's architectural choices as defaults. Record justified changes in architecture decisions rather than silently dropping requirements.

## Operating sequence

1. Inspect the workspace and existing implementation; preserve unrelated work. Discover host access and capabilities. Do not assume that a Windows development workspace is the intended Linux deployment host.
2. Create an execution checklist from P0–P10, including prerequisites, expected outputs, tests, and release gates. Record results in `docs/implementation-status.md`.
3. Complete P0 first: actual Docker SDK creation, persistent-data recovery, quota enforcement, local TLS/routing, storage-account authentication, and optional real GPU testing. Do not install drivers, format disks, or change host networking blindly.
4. Implement P1–P3 as the first working vertical slice: login → scoped resource group → create approved container → real host provisioning → inspect status/logs → stop/delete with explicit storage retention.
5. Add P4–P5 and release the useful MVP: static websites and object storage, private credentials, cost estimates, a working install, and tests on the supported Linux host.
6. Complete P6–P9: PaaS runtimes, database/cache, WAF, all six Linux workspace images, CPU/GPU jobs, and complete accounting. Disable unsupported GPU entries rather than simulating successful execution.
7. Complete P10: package the images and installer, scan and sign releases, run a clean install and restore, perform a supported upgrade, and publish measured acceptance results.
8. Keep multi-host work as a separate release after the complete single-host product is qualified. Preserve node identity, placement, and volume locality in the first release so expansion does not require rewriting resource APIs.

## Non-negotiable behaviors

- A successful deployment creates functioning resources, not only database rows or UI notifications.
- All user-facing actions use the same authenticated API and scope checks. Never rely on portal-only authorization.
- No tenant-controlled Docker socket access, privileged containers, arbitrary host mounts, raw Docker configuration, or host shell command execution.
- The node agent is a highly trusted component. Keep it small, authenticated, and inaccessible from workload networks. Never delete objects belonging to another installation or operator.
- Provisioning is asynchronous, idempotent, crash-recoverable, and capacity-safe under concurrency.
- Resource memory/CPU limits are applied in Docker; persistent storage limits are enforced by a qualified filesystem adapter. Quota numbers in a database alone are insufficient.
- A storage account provides the documented local S3 subset, not Azure Blob/File/Queue/Table compatibility. Linux workspaces are containers, not VMs.
- An attached WAF must have no direct-origin bypass, and its failure must not silently disable protection.
- Assign GPUs by UUID with exclusive reservations initially. Do not claim fractional GPU or VRAM enforcement without a validated supported partitioning mechanism.
- Data and compute have separate lifecycles. Stopping compute retains data and continues storage accounting. Purge is explicit.
- Show allocated cost and measured usage separately. Never double-count an app plan and its children, whole GPU and VRAM, or overhead and its distributed allocation.
- All secrets, logs, metrics, exports, backups, terminal sessions, and resource references obey tenant scope.
- Release configuration uses pinned image digests. Provide SBOMs, scan evidence, license notices, and a supported-version matrix.
- Do not claim a successful hardware, security, restore, or end-to-end test that was not actually run. Report unavailable hardware and remaining qualification clearly.

## First actionable issue set

| ID | Work item | Dependency | Concrete completion evidence |
|---|---|---|---|
| KZ-001 | Inventory target host and trust boundary | None | Saved capability report, confirmed data root, intended DNS/backup configuration, identified missing hardware. |
| KZ-002 | Record architecture and version locks | KZ-001 | Decisions for agent privileges, volumes, network isolation, identity, provider interface, and pinned dependency candidates. |
| KZ-003 | Prove Docker lifecycle | KZ-001 | Programmatically create, observe, stop, recreate, and clean up one owned test container without affecting unrelated workloads. |
| KZ-004 | Prove storage quota and persistence | KZ-001 | Write beyond quota fails; container recreation retains data; existing filesystem remains intact. |
| KZ-005 | Prove S3 account isolation | KZ-003, KZ-004 | Two account endpoints/credentials, scoped object access, key rotation, restart persistence, backup restore. |
| KZ-006 | Prove local ingress/TLS | KZ-003 | LAN client resolves chosen hostname, validates configured certificate, reaches only intended app. |
| KZ-007 | Qualify GPU availability | KZ-001 | Actual container computation and device UUID discovery, or explicit unsupported status. |
| KZ-008 | Scaffold build and platform Compose | KZ-002–KZ-006 | Buildable API/worker/agent/portal images and health-checked database/identity/edge stack. |
| KZ-009 | Implement scoped identity and hierarchy | KZ-008 | Two-tenant positive/negative API tests and usable portal navigation. |
| KZ-010 | Implement reliable container deployment | KZ-009 | Atomic admission, crash recovery, lifecycle actions, real logs/status, and independent host verification. |

These issue IDs cover the starting slice. Generate the subsequent issue set from the phase gates in the master plan and keep its scope traceable to the requested features.

## Acceptance demonstration script

1. Install on a clean supported host; show actual capacity and platform reservation.
2. Create tenant A and tenant B, subscriptions and groups, and users with different roles.
3. Deploy a static site, private storage account, and generic container through the portal.
4. Deploy a PaaS app with a database and cache binding, then protect its endpoint with a WAF.
5. Open a Linux workspace terminal and verify its persistent home survives stop/start.
6. Run a CPU job and, on supported hardware, a GPU calculation; verify resource release and queue behavior.
7. Show that tenant B cannot access tenant A's API objects, endpoint credentials, logs, cost exports, terminal, or backups.
8. Trigger a quota failure and a concurrent admission conflict; show accurate errors and unchanged healthy resources.
9. Restart the worker and host; verify desired-state recovery and persistent data.
10. Stop an application and show compute allocation ending while retained storage continues to accrue showback.
11. Back up and restore to a clean installation; verify identities, data, and usable secrets/keys under the documented recovery procedure.
12. Build the release bundle, verify image digests, and execute a supported upgrade.

## What to report at each milestone

Report completed functionality, exact verification evidence, known limitations, changed assumptions, produced images/artifacts, and the next gated task. Distinguish development demos from qualified releases. Keep the portal honest about provisioning failures, unavailable capacity, and unsupported capabilities.
