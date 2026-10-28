# Kzure implementation status

Last updated: October 27, 2026. Release: `0.1.0-dev`.

**Development resumed at the user's request.** The platform and retained workloads were restored after their containers had been removed. IPv4 subnet enforcement passed 17 integration groups; optional loopback HTTPS passed seven checks; see [REMOTE_ACCESS_PROGRESS.md](REMOTE_ACCESS_PROGRESS.md) for actual evidence and remaining release gates.

Final resumed verification: 16 foundation, 28 browser-shell, 14 native-access/network, six desktop and 17 IPv4 policy groups passed, together with seven HTTPS/WSS checks, Go tests/vet and the portal build. Metadata restored into a scratch database with 83 resource records. This is a working single-host development milestone, with the wider master-plan gates still open.

**October 27 feature re-analysis (baseline `0a10e08`):** the [core gap plan](CORE_FUNCTIONALITY_GAP_PLAN.md) now contains a current capability matrix, resolved/open source findings and nine ordered implementation slices. Earlier missing-feature claims for custom images, revisions, resize and retained restore have been removed. The next gates are operation/network recovery, application hostname/TLS ingress and managed data bindings, followed by workload backups, physical disk protection and clean-host recovery. Portal authentication enhancements remain deferred. This pass changed documentation only and reran no workload tests.

**Subsequent implementation:** custom image deployment/configuration, stable assigned local application ports, application revisions/update/CPU-RAM resize/rollback and retained-volume restore are now implemented. The application suite passed 19 groups; public pull, custom-port private connectors and unmanaged image-volume rejection passed three more. See [APPLICATION_PROGRESS.md](APPLICATION_PROGRESS.md). General application hostname/TLS ingress, complete service bindings, provider-aware workload backups and the remaining F01–F12 gates are still open.

**Subsequent reliability implementation:** observed firewall drift repair, independent periodic maintenance, conditional lease heartbeats, locked initial state transitions and agent checks of current operation ownership before guarded lifecycle mutations are implemented. See [RELIABILITY_PROGRESS.md](RELIABILITY_PROGRESS.md) for evidence and limitations. The next application milestone is hostname/TLS ingress and managed data bindings. Full in-flight/distributed fencing and complete F07/F08 qualification remain open.

**Subsequent application ingress:** static sites and HTTP container apps now have managed loopback HTTPS hostnames, readiness-gated routing, an Endpoint tab, persistent application CA/leaf renewal and gateway recovery. Thirteen ingress groups passed. See [APPLICATION_INGRESS_PROGRESS.md](APPLICATION_INGRESS_PROGRESS.md). Supplied certificates/custom domains, replicas and LAN qualification remain; managed data bindings are next.

**Subsequent managed bindings:** the portal now supports app-to-PostgreSQL/Valkey/S3 bindings with stable private aliases, encrypted credential snapshots, revision-based bind/unbind and saved binding rollback. The example HTTP app performs real operations against all three services over managed HTTPS. See [APPLICATION_BINDINGS_PROGRESS.md](APPLICATION_BINDINGS_PROGRESS.md). Workload backups/restore and physical storage protection are next; provider credential rotation/upgrades remain open.

**Subsequent workload recovery:** encrypted local stopped-volume snapshots and fresh-volume restore are implemented for PostgreSQL, Valkey, S3, plain Linux workspaces and persistent container apps. Twelve recovery integration groups passed; see [WORKLOAD_RECOVERY_PROGRESS.md](WORKLOAD_RECOVERY_PROGRESS.md). Off-host/full-installation recovery, larger archives and complete provider/hardware qualification remain open. The explicit [fresh reinstall workflow](FRESH_INSTALL.md) wipes installation data/configuration and regenerates credentials.

## Delivered foundation

- React/TypeScript portal: sign-in, dashboard, tenant switcher, catalog, real create-resource wizard, resource details, logs, live metrics, lifecycle actions, group/tenant/subscription creation, operations, cost table/CSV, host capability view, and scoped local-user creation.
- Go API, durable PostgreSQL operation worker, periodic desired-state reconciliation, and a separate token-authenticated Docker agent.
- Actual CPU/RAM admission and runtime limits, transaction-serialized quota checks, idempotency with body mismatch detection, encrypted service passwords/keys, owner-gated secret reveal, and managed object ownership validation.
- Catalog providers: static web, initial approved container app template, PostgreSQL, Valkey, dedicated S3 storage, and six Linux workspace distributions.
- Custom platform, static-site, storage-account, and workspace Docker images; image identity lock; pinned upstream build bases; Compose bootstrap and private generated configuration.
- Allocation intervals, elapsed showback, retained-storage accounting, and real Docker logs/metrics.
- Automated unit and integration verification with private local test reports. See the verification report for actual run results; no unexecuted checks are represented as passed.
- Browser shell across six minimal and six SSH workspace variants: non-root PTY, resize/Ctrl-C/Unicode, reconnect/fullscreen, explicit contributor grants, active-session termination, short-lived single-use tickets, audit metadata, output flow control, and local cleanup after agent failure. The final 28-group terminal integration suite passed; see [REMOTE_ACCESS_PROGRESS.md](REMOTE_ACCESS_PROGRESS.md).
- Remote access and networking: six SSH workspace variants, restricted native bastion, public-key management, private subnets/IPAM, DNS/service connectors, Ubuntu desktop and browser RDP. Native/network verification passed 14 groups, desktop verification passed six groups and IPv4 policy verification passed 17 groups. A real graphical desktop was verified in the portal. Wider failure, recovery and dedicated-host gates remain open.

## Master-plan gate status

| Phase | Status | Remaining gate work |
|---|---|---|
| P0: host proof | Partial | Dedicated Linux host with quota-capable filesystem, physical storage inventory and GPU qualification. Optional loopback portal TLS is tested; application ingress/LAN qualification remain. |
| P1: bootstrap/design | Partial | API contract completeness, full design documentation and clean supported-Linux install qualification. |
| P2: identity/governance | Partial; portal auth upgrades deferred | Quota editing, tags, deletion locks, group lifecycle and templates remain functional work. Existing local sessions and API tenant boundaries work. OIDC/MFA/SSO, account redesign and deeper identity hardening are outside the current functional sequence. |
| P3: resource engine | Partial | Custom application images/configuration, revisions/rollback, CPU/RAM resize and retained restore are delivered. Lease heartbeats, guarded lifecycle dispatch and independent reconciliation are delivered. Full spec drift, other-provider updates, retry/cancel and distributed/in-flight side-effect guarantees remain. |
| P4: static apps | Partial | Managed loopback TLS is delivered. Archive builds, static revisions/rollback, supplied custom domains and certificates remain. A single HTML/default site deploys to a real container. |
| P5: storage/MVP | Partial | Object browser, key rotation, managed-volume inventory/reattach, physical usage/admission, enforced quota and workload restore. Signed S3 operations and metadata scratch restore are tested. |
| P6: PaaS/data | Partial | Managed data bindings are delivered. Source builds, registry, app plans/runtime recipes, provider configuration/credential rotation, data-service upgrades and database backup/restore remain. Dedicated database/cache providers and fixed TCP connectors run. |
| P7: WAF/policy | Partial | IPv4 subnet policy, UI and 17 packet-test groups delivered. Observed firewall repair is delivered; WAF provider, wider egress qualification and no-bypass ingress remain. |
| P8: Linux/GPU jobs | Partial | Browser shell works across six minimal and six SSH variants, plus a graphical desktop. CPU batch/scheduled jobs, artifacts, GPU inventory/reservations and real device execution remain. |
| P9: costing | Partial | Editable effective-dated rates, budgets, GPU meters, outage uncertainty, overhead pools. Fixed-rate numeric allocation accounting and CSV are present. |
| P10: release | Partial | Full recovery/upgrade drill, SBOM/vulnerability/signature checks, accessibility qualification, offline bundle and production hardening. Development image builds and localhost boot work. |
| R3: multiple hosts | Planned | Enrollment/mTLS, placement, data locality/migration, fencing and HA design. |

## Current execution priorities

Use [F01–F12 in the core gap plan](CORE_FUNCTIONALITY_GAP_PLAN.md), rather than the historical pause checklist, for new work. Portal authentication enhancements are deferred; no authentication changes or feature implementation were made during this planning pass.

1. **Complete application hosting:** the initial F01/F03 custom-image/configuration/revision/resize/rollback workflow is built. The loopback F02 hostname/TLS and PostgreSQL/Valkey/S3 binding journey is built. Preserve its qualification while finishing remaining endpoint modes/replicas; extend F08 reconciliation and recovery.
2. **Recover data:** retained resources now have a Restore action that reuses existing data safely. Deliver provider-aware workload backup/restore and a clean installation recovery drill; retained data on the same disk is not a backup.
3. **Protect physical capacity:** F05 disk usage/headroom/admission and qualified quotas; account for backups/images/builds as well as declared workload data.
4. **Finish everyday service workflows:** F06 multi-file static sites, object browser and database/application bindings; F07 outbound routing/DNS and wider host qualification (observed-policy repair is delivered); F09 quota editing, tags, templates and dependency-aware management.
5. **Make operations visible and reliable:** extend F08 retry/cancel, reconciliation, health history/alerts, upgrades and clean-host acceptance throughout the work.
6. **Complete requested service families:** source-build PaaS, F10 WAF and F11 CPU/GPU jobs; preserve explicit hardware qualification gates.
7. **Complete cost analysis:** F12 editable rates, date ranges, budgets, GPU and infrastructure overhead alongside the resources they meter. Current fixed-rate showback is a starting point.

The core missing journey is custom app → stable endpoint → private database/storage binding → safe update/resize → monitoring/cost → backup/restore. Multi-host and HA follow a useful single-host release. Existing Linux resources remain containers; true VM support would need a separate hypervisor provider.

## Operator notes

All persistent configuration lives in the local workspace/owned Docker volumes. `.env` and `.local/` contain private installation material and are ignored by Git. The browser-facing development endpoint is `http://localhost:8088`. Demonstration resources created by the smoke test are intentionally left available for inspection. Isolated test resources may appear in historical activity/cost views after deletion, as required by allocation history.

