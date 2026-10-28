# Kzure core functionality gap analysis and delivery plan

Reviewed: October 27, 2026. Source baseline: commit `0a10e08`. This review supersedes the original `85290f2` inventory and credits the application work delivered in `8738f76`.

This is the execution plan for the single-host product, alongside the original [project specification](../PROJECT_PLAN.md). Evidence comes from current API, models, agent, worker, portal, backup/release scripts and recorded verification. This pass changes documentation only: it does not rerun integration suites or qualify new hardware. [Application progress](APPLICATION_PROGRESS.md) and [remote-access progress](REMOTE_ACCESS_PROGRESS.md) retain detailed test evidence.

**Portal authentication enhancements remain deferred by user instruction.** Preserve existing access checks. Workload credentials, safe recovery, network enforcement and capacity control remain in scope.

## Subsequent reliability implementation — October 27

Slice 1 now has implemented observed firewall repair, independent periodic maintenance, operation lease heartbeats, guarded database transitions, agent lifecycle serialization and durable ownership checks before guarded Docker mutations. See [RELIABILITY_PROGRESS.md](RELIABILITY_PROGRESS.md) for actual tests and scope. Full daemon/in-flight ambiguity and distributed side-effect fencing remain open; this does not complete all of F07/F08. Next application work is slice 2 hostname/TLS ingress, with backup design alongside it.

## Subsequent application ingress implementation — October 27

Managed loopback hostnames/TLS, readiness-gated route replacement, static/app routing, retained endpoint identity, persistent local CA and portal endpoint controls are implemented. The ingress suite passed 13 groups; see [APPLICATION_INGRESS_PROGRESS.md](APPLICATION_INGRESS_PROGRESS.md). Replicas, supplied certificates/custom domains and LAN qualification remain. Continue to slice 3 managed data bindings; M1 stays in progress.

## Subsequent managed data bindings — October 27

Application/PostgreSQL/Valkey/S3 bindings, encrypted immutable credential snapshots, prefix validation, revision-based bind/unbind and connector recovery are implemented. The example HTTP app completed actual database writes, signed object round trips and cache operations over managed HTTPS. See [binding progress](APPLICATION_BINDINGS_PROGRESS.md) for current verification and remaining provider rotation/recovery gates. Continue to provider-aware workload backups and physical capacity protection.

## Subsequent workload recovery — October 27

Encrypted stopped-volume backup jobs, the Backups tab and checksum/authentication-verified restore into fresh volumes are implemented. Twelve integration groups passed across PostgreSQL, Valkey, S3, plain workspaces and a persistent custom application. See [workload recovery progress](WORKLOAD_RECOVERY_PROGRESS.md). The initial limit is 512 MiB; off-host export/full installation recovery, larger archives, schedules and the remaining provider identities are still open. Continue to physical storage protection and complete recovery bundles.

## Assessment

Kzure now hosts custom applications with configuration, encrypted secret references, revisions, CPU/RAM resize and rollback. Managed local HTTPS and private PostgreSQL/Valkey/S3 bindings complete the basic application/data journey. Shell, SSH/bastion and browser desktop remain implemented. The next core gap is **recovering workload contents into fresh volumes**, followed by physical disk protection and everyday provider management.

The release remains a single-host development preview. WAF, source-build PaaS and GPU providers are still missing. Continue to data protection and operational workflows while completing the catalog; portal SSO remains deferred.

## Current capability inventory

“Partial” means usable subfeatures exist, but the full work-package acceptance gate remains open. Recorded checks qualify their documented development-host scope only; they are not production or Linux-server acceptance.

| Area / package | State and delivered functionality | Remaining gap / practical effect |
|---|---|---|
| Organization — F09 | Partial: tenants, subscriptions, groups, scoped creation/listing and admission quotas | Quota editing, tags, locks, safe group teardown and reusable stack templates; routine management still requires individual actions |
| Custom containers — F01 | Partial: registered local/public Linux images, immutable retained identity, service/continuous-worker modes, environment/secrets, command, readiness and one managed mount | Private-registry credentials, archive import, image availability/retention management, secret rotation; finite jobs belong to F11 |
| Application endpoints — F02 | Partial: stable local ports plus managed loopback hostname/TLS gateway, readiness gating, persistent CA and portal endpoint controls | Supplied certificates/custom domains, wider exposure modes, replicas and Linux/LAN qualification remain; application TLS now has its own CA |
| Lifecycle — F03 | Partial: app revisions, conditional edits, image/config updates, CPU/RAM resize, failed-update recovery and rollback | Other-provider updates, storage growth/migration, observed spec reconciliation and route-based rollout; current replacement interrupts compute |
| Recovery — F04 | Partial: retained restore plus encrypted local stopped-volume snapshots and fresh-volume recovery for supported providers | Larger archives, off-host export, volume inventory, schedules and complete installation recovery; initial recovery limit is 512 MiB |
| Disk capacity — F05 | Declared-only storage admission; CPU/RAM limits are real | Physical bytes/inodes, headroom, image/log/backup overhead and qualified hard quotas; a workload can still exhaust shared disk |
| Static web — F06 | Partial: real Nginx with one HTML page | Multi-file upload, SPA routing, revisions and rollback; ordinary frontend bundles lack a supported deployment workflow |
| S3 / data services — F06 | Partial: dedicated S3, PostgreSQL and Valkey, generated credentials and private connectors | Managed connection settings are now implemented. Object browser, credential rotation, provider-aware upgrades and recovery remain |
| Source-build PaaS — F06 | Not implemented; catalog entry unavailable | Isolated builder, registry, app service plans, runtime recipes, build logs and source-to-revision workflow |
| Networking — F07 | Partial: bridges/IPAM, connectors, associated IPv4 policy | Observed owned firewall repair is now implemented. Outbound-access modes, DNS management and topology/effective-rule diagnostics remain |
| Remote access — F07/F08 | Implemented in development scope: browser shell, six SSH variants, bastion and browser RDP desktop | Native RDP client qualification, reconnect/failure matrix, effective-access diagnostics and image-upgrade continuity testing |
| Operations — F08 | Partial: durable leases, missing-container reconciliation, fenced database completion/failure writes, preserved workspace image history | Lease heartbeats, per-resource agent serialization, durable ownership checks and independent maintenance are now implemented. Full in-flight/distributed side-effect guarantees, retry/cancel, health history, upgrades and offline/recovery image inventory remain |
| Costs — F12 | Partial: fixed CPU/RAM/storage rates, allocation ledger, current metrics and CSV | Editable dated rates, date filters, budgets, utilization comparisons, outage uncertainty and full GPU/infrastructure costs |
| WAF — F10 | Not implemented; catalog entry unavailable | Deployable policy/gateway, request events and enforced origin isolation |
| CPU/GPU jobs — F11 | Not implemented; GPU catalog entry unavailable | Finite/scheduled jobs, artifacts, inventory, exclusive GPU allocation and real device execution; hardware qualification required |
| Release / multi-host | Single-host development build, image locks/export and metadata recovery checks | Clean Linux install/upgrade/complete recovery; multiple hosts and HA remain later work |

## Source-backed findings and resolved findings

1. **Custom deployment is implemented.** [applications.go](../internal/platform/applications.go) registers images and secret metadata; [model.go](../internal/platform/model.go) now has application configuration fields. The remaining default Nginx catalog entry does not mean custom images are unsupported. [ApplicationOptions.tsx](../apps/portal/src/ApplicationOptions.tsx) exposes the workflow.
2. **Application updates are implemented with bounded scope.** [configuration.go](../internal/platform/configuration.go) provides revision listing, PATCH and rollback. It restricts updates to container apps and rejects mount, storage-size and subnet changes. Changing port/mode with existing connectors requires removing those connections. Extend capability-specific contracts instead of assuming general resize exists.
3. **Retained restore exists; backups do not cover workloads.** [api.go](../internal/platform/api.go) accepts restore; [agent.go](../internal/platform/agent.go) refuses missing retained volumes. [backup.mjs](../scripts/backup.mjs) explicitly excludes workload volumes, images and recovery keys. Metadata row-count verification cannot establish application-data recovery.
4. **Local application port identity is fixed.** The API reserves ports transactionally. Managed application endpoint records and loopback hostname/TLS now exist. Supplied domains/certificates, replicas and wider exposure qualification remain F02 work.
5. **Storage admission remains declarative.** The host contract reports CPU/RAM but not physical storage headroom or inodes. F05 must measure/enforce actual capacity and include images, logs, backups and build scratch.
6. **Worker database fencing improved; operation recovery is unfinished.** [worker.go](../internal/platform/worker.go) checks the claimed attempt in both completion/failure transactions. The subsequent reliability slice adds lease renewal, locked initial transitions, independent maintenance and agent ownership checks before guarded resource mutations. Partial Docker effects/in-flight ambiguity still need wider fault qualification before distributed concurrency claims.
7. **Observed firewall drift repair is implemented.** [agent_policy.go](../internal/platform/agent_policy.go) now inspects actual owned hooks/rules even when the desired generation is unchanged. The adapter fingerprints canonical observed state and repairs missing/reordered hooks and altered owned chains. This finding is resolved for the tested single-agent development scope; wider host/IPv6 qualification remains.
8. **Image approval history is preserved, but recovery export is incomplete.** Full image bootstrap now retains workspace history. [release.mjs](../scripts/release.mjs) enumerates current catalog/platform images and excludes history; it does not build a closure of registered/deployed/revision images from metadata. Recovery must include every image needed by restored workloads and supported rollback revisions.
9. **Managed service bindings are implemented.** Endpoint/secret references, ownership, bind/unbind revisions and connector recovery are qualified by an actual API writing PostgreSQL rows, signed S3 objects and Valkey counters over managed HTTPS. Provider credential rotation and upgrades remain F06 work.
10. **Higher catalog features remain unavailable.** WAF, GPU and source-build PaaS have unavailable entries in the model. Current metrics and fixed-rate accounting are useful foundations, not complete monitoring or capacity/cost analysis.

## Next implementation backlog

Execute these slices in order; limited independent documentation/UI work can accompany them. Existing resources and private installation material must be preserved. A slice closes only when its API, Docker behavior, portal flow, failure tests and progress record agree.

| Order / slice | Concrete deliverables | Completion evidence |
|---|---|---|
| 1. Reliability prerequisites — F08/F07 (implemented in documented scope) | Inspect/repair observed firewall state; prevent stale operation attempts from mutating current resources; keep policy/health reconciliation responsive during slow deployment | Remove an owned firewall hook without changing its desired generation; show automatic repair. Inject delayed/stale attempts and slow work without stale resource effects or stalled policy repair |
| 2. Application ingress — F02 (loopback slice implemented) | Versioned endpoint/route records, shared edge without Docker socket, local hostname/TLS workflow, private/loopback exposure first, certificate/route diagnostics | Same URL survives update/recreation; unhealthy backend excluded; invalid route/certificate change retains the last working route; unrelated routes unaffected |
| 3. Application + data bindings — F01/F06 (initial implementation delivered) | PostgreSQL/Valkey/S3 references, bind/unbind revisions, encrypted snapshots and connector recovery delivered; provider rotation and advanced diagnostics remain F06 | Verified custom API writes rows/objects/cache values over managed HTTPS, app recreation, unbind and rollback, with secret-free metadata/logs. Provider rotation and complete target upgrade qualification remain open |
| 4. Recoverable data — F04 | Managed-volume inventory, provider-aware backup jobs/destinations/checksums, fresh-volume restore wizard; begin PostgreSQL and generic quiesced file data, then S3/Valkey | Restore known rows/objects/files into fresh volumes; reject corrupt/incompatible backups; verify recovery keys and required image inventory; preserve original resource on failure |
| 5. Disk and release recovery — F05/F08 | Physical usage/headroom admission, bounded logs/cache, compatible-host quotas; complete metadata + keys + images + workload recovery bundle and instructions | Low-space/inode tests, owned-only cleanup, clean-host restore drill and measured recovery time/data loss; real Linux quota proof remains a separate hardware/environment gate |
| 6. Everyday cloud management — F06/F07/F09 | Multi-file static sites, object browser, outbound/DNS controls, rule forms, quota editor, tags/locks, dependency-aware templates and group deletion | Recreate a frontend/API/database/storage stack in a new group through supported UI/API; preview and execute teardown preserving retained data |
| 7. Replicas and diagnostics — F02/F03/F08/F12 | Stateless replicas with per-replica admission/charging, readiness-based route switch, health history, actionable failure/retry controls, editable cost rates and date filters | Two healthy replicas serve traffic; failed replacement preserves service; cost intervals reconcile across resize/replica/rate changes and telemetry outages |
| 8. Complete service catalog — F06/F10/F11/F12 | Bounded source builds/registry/app plans; WAF with no origin bypass; finite CPU jobs then exclusive GPU jobs; budgets and complete overhead meters | Build/deploy/rollback source; normal/blocked WAF traffic plus failed-origin-bypass tests; job completion/cancel/artifacts; actual GPU computation and no double allocation |
| 9. Single-host release qualification — F08 | Supported Linux clean install and upgrade, full regression/fault matrix, offline workload-image coverage, image/license/SBOM/scan evidence, portal accessibility checks | Versioned release evidence records exact platform, images, test results and limitations. No production claim based solely on development-host tests |

Milestone M1 requires slices 1–3 plus the existing application update/retained-restore behavior. M2 requires slices 4–7, including clean recovery and daily management. M3 requires slices 8–9. Start backup design during M1 so ingress work does not postpone data protection indefinitely. Hardware-gated tests must stay explicitly unqualified rather than being counted as passed; CPU-only work can proceed without a GPU.

## Evidence freshness and exit criteria

- Application implementation: 19 recorded integration groups plus three public-pull/custom-port/image-volume groups; Go tests/vet and portal build passed in the implementation pass.
- Foundation 16, browser shell 28, IPv4 policy 17 and HTTPS/WSS seven groups/checks were rerun after application changes. Native/network 14 and desktop six are earlier milestone evidence and were not rerun during that application pass.
- The later metadata restore verified 106 resource records. This remains metadata-only and does not qualify workload disaster recovery. Counts are historical test evidence, not current live inventory.
- README screenshots show actual UI but are not feature acceptance tests. This analysis ran no new workload tests and made no runtime changes.
- Do not publish a single completion percentage: providers, hardware tests and operational gates have different scope. Track each slice as planned, in progress, implemented, qualified for a named environment, or hardware blocked, with its evidence and remaining work.

## Priorities and work packages

Priority A completes a useful daily-use platform and protects user data. Priority B completes the explicitly requested single-host service range. Priority C is an expansion after that. “Implemented” requires API, actual Docker behavior, usable portal controls, documentation and acceptance evidence; a catalog entry is insufficient.

### F01 — Deploy your own containers (A)

**Dependencies:** Extend the resource contract and operation fencing in F03/F08 alongside the first vertical slice; do not wait for a broad rewrite.

- Add operator-approved OCI image registration/import, digest resolution, architecture validation and local image availability status. Support public image pulls first, then private-registry credential references; never place registry credentials in source URLs or logs.
- Add validated environment variables, versioned secret references, command/arguments, container ports, probe settings and managed-volume mounts. Keep host paths, raw Docker JSON and privileged mode outside the ordinary wizard.
- Separate web services, background workers and one-off jobs in the resource model. Define restart behavior and successful exit semantics.
- Add an application wizard with capacity estimate, networking/storage choices, pull progress and actionable failure details. Track deployed digest independently of the current catalog default.
- **Accept:** Deploy a small custom API and a non-HTTP worker from images, inject a secret without exposing it in logs/export, persist application data, reject an unsupported architecture, recover from a failed pull and recreate the resource using the same immutable digest.

### F02 — Stable app endpoints, ingress and service discovery (A)

**Dependencies:** F01 service/port contract; existing private networking. WAF uses this path later.

- Add managed endpoint records with stable names, explicit private/localhost/LAN exposure choices and port ownership. Preserve service identity when containers are replaced.
- Add a shared managed HTTP(S) edge with per-application hostname routes, readiness gating and atomic configuration changes. Support local DNS/hosts guidance, supplied/local certificates, expiry visibility and renewal workflow.
- Keep databases/caches private by default; make direct TCP publishing explicit. A connector should supply a stable endpoint even after target recreation, with clear credential binding.
- Support fixed replica count for stateless services, per-replica admission/cost and healthy-backend balancing. Defer autoscaling until metrics and update semantics exist. Do not duplicate stateful volumes across replicas.
- **Accept:** A bookmarked application URL remains valid through restart, recreation and revision rollout; an unavailable backend is not routed; two replicas balance traffic; a private database has no unintended host listener; invalid route/certificate updates preserve the previous working configuration. Validate LAN separately when enabled.

### F03 — Resource configuration, updates and lifecycle (A)

**Dependencies:** Existing resource engine; F01 supplies the first application update case.

- Introduce versioned desired specs, observed/deployed generations, immutable revision records and conditional updates. Add explicit provider capabilities instead of assuming every action works for every type.
- Add CPU/RAM resize and grow-only managed storage, with transactional admission and a downtime preview. Recheck quotas for image/config updates, additional replicas and retained-volume attachment.
- Implement safe replacement: create candidate revision, verify readiness, switch traffic, retire old compute and retain the last usable revision. Rollback never claims to undo database migrations.
- Reconcile image/spec drift, not just missing/stopped containers. Define restart-required versus live-updatable settings and preserve persistent attachments.
- **Accept:** Change environment and image, resize within limits, reject concurrent overbooking, reject a stale edit, roll back a failed app rollout and preserve resource identity/data. Unsupported actions are absent or clearly disabled in the portal.

### F04 — Retained data, backup and restore (A; start early)

**Dependencies:** Existing volumes and secrets; F03 attachment/revision model. Recovery is required before valuable workload data is entrusted to the platform.

- Register volumes as first-class managed objects with owner, attachment, capacity, usage, retention state and compatibility. Add retained-data inventory, restore-to-resource and explicit purge; prevent accidental multi-writer attachment.
- Add consistent PostgreSQL backup/restore, S3 data plus metadata backup, workspace/desktop data backup and documented Valkey durability/restore behavior. Use provider-aware procedures, not blind live-database directory copies.
- Add backup jobs, schedules, retention, checksums, capacity accounting and operator-selected destination. Prefer a separate disk/host for a recovery copy; label same-disk copies accurately.
- Restore into a new resource by default with a preview of conflicts and required recovery keys. Build full installation recovery for metadata, private recovery material, image identities and workload data.
- **Accept:** Write a database row, object and workspace file; back up; restore into fresh volumes and verify content. Recover a retained resource through the UI. Drill a clean installation restore and measure actual recovery time/data loss. A metadata row-count match alone does not pass.

### F05 — Real storage capacity and disk protection (A)

**Dependencies:** F04 volume registry; quota enforcement needs a prepared compatible host filesystem.

- Inventory the actual Docker data/storage pool, free bytes/inodes, volume usage, images, logs, build scratch and backups. Show declared, reserved and physically used storage separately.
- Add host storage headroom/admission, low-space warnings and bounded temporary/build/image-cache consumption. Include infrastructure and backup overhead in capacity checks.
- Implement a qualified hard-quota adapter and storage classes on a supported Linux host; prove quota enforcement and safe grow. Do not automatically format an existing disk. Continue honest declared-only mode on unqualified Docker Desktop storage.
- Define safe cleanup with previews and ownership checks; never prune unrelated Docker resources. Expose logical S3 usage separately from physical overhead.
- **Accept:** A workload hitting quota cannot fill the platform database's reserved space; growth cannot overcommit physical capacity; disk/inode exhaustion yields a recoverable error; retained data remains accounted; cleanup touches only selected owned objects. Desktop simulation does not count as a Linux quota proof.

### F06 — Complete web, storage and PaaS service workflows (A/B)

**Dependencies:** F01–F04 for image/revision/binding foundations.

- **A: Static sites:** upload a multi-file site archive with size/path/symlink/expansion checks, SPA routing choice and revision rollback. Serve CSS, JS and images correctly.
- **A: Storage accounts:** list/create buckets, browse/upload/download/delete objects, report usage and show client connection examples. Add versioned account keys and rotation; clarify that this service speaks S3.
- **A: Data services:** edit supported PostgreSQL/Valkey settings, expose health and connection diagnostics, manage application connection bindings, and provide guarded version upgrades after a backup. Expand database/user management only to supported provider operations.
- **B: App service:** local OCI registry and isolated bounded build worker; source archives first, explicit Git sources afterward; Node/Python/.NET runtime recipes; build logs/artifacts; deploy by digest; scheduled image/cache cleanup. Java and additional recipes follow the same contract.
- **Accept:** Deploy a multi-file frontend and custom API, connect the API to PostgreSQL and S3 using managed bindings, rotate a service credential without leaking it, build a changed source revision, and roll back the application while preserving its data.

### F07 — Finish usable network management and remote access (A)

**Dependencies:** Existing IPv4 adapter and bastion; F02 endpoint contract.

- Add an explicit outbound-access model for private subnets: offline, approved destinations, or operator-managed internet egress/NAT. Handle DNS resolution/package downloads deliberately; an “allow” rule alone does not create a route on an internal Docker network.
- Add private DNS records/service names, subnet/address availability views and a simple topology showing endpoints, connectors and policies. Explain failed connectivity using observed routes, bindings and rules.
- Inspect/reconcile actual policy hooks, recover owned bridges after disruption, reject overlapping operator/VPN/LAN ranges and test application published ports as well as bridge traffic. Surface a pending/failed policy accurately.
- Verify native SSH and an actual native RDP client via bastion; test target replacement, certificate mismatch, restart and session cleanup. Add effective-access/session diagnostics and preserve host identities through image upgrades.
- **Accept:** Required app-to-database and package-download paths work only under configured policy; management/other-tenant paths remain blocked; removal of an owned firewall hook is detected and repaired; unrelated firewall rules survive; native RDP reaches the real desktop. Qualify dedicated Linux/LAN independently. IPv6 stays explicitly unsupported until tested.

### F08 — Reliable operations, observability and upgrades (A)

**Dependencies:** Extend continuously with F01–F07; no portal-auth dependency.

- Fence success and failure writes, persist operation steps, classify transient/permanent errors, cap retries with backoff, add cancel/retry controls and show partial effects. Give leases heartbeat/expiry semantics appropriate to long builds.
- Keep periodic reconciliation responsive while a deployment/build is slow. Recover partial networks/volumes/containers without duplication; preserve last good revisions and user data.
- Add resource/host health history, CPU/RAM/disk and later GPU metrics, restart/OOM/exit reasons, bounded log streaming/download, operation correlation and actionable in-portal alerts. Report stale telemetry explicitly.
- Add versioned database migrations, upgrade preflight/backup, rollback compatibility notes, image provenance and full image-lock history preservation. Qualify clean install and upgrade on supported Linux; package offline images/checksums and license/SBOM/scan evidence.
- **Accept:** Interrupt each deployment phase, restart agent/worker/Docker, inject delayed stale-worker results and a transient pull error; operations recover or end with a clear retry path, without duplicate allocations/data loss. A slow build does not stall health reconciliation. Re-run full image bootstrap with existing minimal/SSH workspaces and verify shells still work. Perform an upgrade and restore drill from the previous release.

### F09 — Cloud management workflows and repeatable deployments (A)

**Dependencies:** F01–F04 contracts; retain existing tenant hierarchy.

- Edit subscription CPU/RAM/storage quotas, show remaining capacity and explain why a deployment cannot fit. Model GPU/count limits when F11 lands.
- Add tags, search/filter/pagination, dependency views, deletion locks, explicit group teardown preview and clear retained-data consequences. Add supported hierarchy rename/delete/move operations with scope validation.
- Add saved templates and export/import for app + database + storage + network, parameterized secrets and a deployment preview with resource/cost changes. Start with a small documented JSON contract and CLI; Terraform/ARM compatibility is not required.
- Replace raw JSON network-rule entry with form rows and effective-policy display. Correct stale feature text and make logs, costs, mounts, endpoints, revisions, backups and operations discoverable from resource details.
- **Accept:** Recreate a sample stack in another group using a saved template, view its dependencies and estimate, adjust a quota, find resources by tag and preview safe group deletion without manual Docker/database edits.

### F10 — Real WAF service (B; explicitly requested)

**Dependencies:** F02 ingress and F03 revisions.

- Build/pin the WAF image and supported rule set; provide attach/detach, detection/blocking modes, exclusions and bounded request/security logs.
- Enforce edge → WAF → origin with no published origin port or alternative edge route. Apply configuration atomically, validate certificates/health and account for gateway capacity.
- **Accept:** Normal traffic works, representative malicious requests are blocked in blocking mode, excluded requests behave as configured, origin bypass fails and a WAF failure never silently opens an unprotected route.

### F11 — CPU jobs and actual GPU compute (B; explicitly requested)

**Dependencies:** F01 execution contract, F08 durable jobs, F12 metering. GPU validation requires actual compatible hardware/runtime; CPU jobs can ship first.

- Add finite jobs, exit/result status, artifacts, timeout/cancel, retry policy, cron scheduling and a bounded queue. Successful batch completion must not restart forever.
- Discover real GPU model/UUID/VRAM/runtime health; add exclusive whole-device reservations and Docker device requests. Provide approved CUDA job/workspace images and dataset/output volumes.
- Release GPU reservations only after confirmed process/device release; persist them through worker restart. Track device utilization/VRAM separately from allocation price.
- **Accept:** A CPU job finishes and retains artifacts; a scheduled job runs once per intended execution; a CUDA computation uses the assigned GPU; simultaneous requests cannot allocate one device twice; cancel/crash/restart release or recover reservations correctly. Show “unqualified/unavailable” until hardware tests pass.

### F12 — Useful local costs and capacity analysis (A baseline, B GPU/advanced)

**Dependencies:** Extend existing allocation ledger immediately; consume actual F04/F05/F11 inventory as delivered.

- Add editable, versioned, effective-dated CPU/RAM/storage rates; retain historical rate application. Add GPU class rates when hardware allocation exists. Keep the example rate-card file clearly separate from the actual active configuration until implemented.
- Offer date-range totals and breakdowns by tenant/subscription/group/resource/type, allocation versus utilization, predicted monthly cost and CSV export. Expose idle capacity and a what-if size estimate.
- Include replicas, retained data, backups, connectors, WAF, guacd, builds and registry/cache overhead exactly once. Show shared platform costs as an explicit pool. Keep the bastion bundle's existing CPU/RAM split from being double counted.
- Add budget thresholds/alerts, cap changes and outage uncertainty/reconciliation. Budget default is notification, not destructive shutdown. Hardware amortization/energy inputs are optional; avoid claiming measured electricity cost without measurements.
- **Accept:** Deterministic decimal tests cover create/resize/rate change/stop/retention/purge, multiple replicas, infrastructure overhead and an observation outage. Totals reconcile with detailed intervals and physical allocations. GPU VRAM is not charged again on top of an exclusive whole-device rate unless a different billing model is explicitly chosen.

## Delivery order and completion gates

| Milestone | Order and scope | Exit gate |
|---|---|---|
| M0 — Existing foundation | Complete within its documented development scope | Preserve existing real Docker, shell, SSH, desktop and IPv4 test evidence; do not relabel it as full product acceptance |
| M1 — Host my own application | Slices 1–3: F08 side-effect fencing/F07 policy repair → F02 hostname/TLS → F06 data bindings; preserve delivered F01/F03 and retained restore; design F04 backups alongside | Deploy a custom app + database with persistent data, stable URL, safe update/rollback, correct capacity/cost and a usable retained-data recovery path |
| M2 — Keep and recover real workloads | Slices 4–7: F04 backups/restore → F05 disk safety and clean recovery → F06/F07/F09 everyday workflows → F02 replicas, F08 diagnostics and F12 rate analysis | A normal operator can deploy, diagnose, back up and restore a useful multi-resource stack through supported UI/API workflows; complete a clean-host restore drill |
| M3 — Complete requested single-host catalog | Slices 8–9: F06 source-build PaaS → F10 WAF → F11 CPU/GPU jobs; finish F12 budgets/GPU/overhead and Linux release qualification | Every originally requested service family is backed by working local resources and actual acceptance evidence; explicitly record any hardware-dependent blocked gate |
| M4 — Multi-host expansion | Node inventory/enrollment, placement, cross-host networking, volume locality, drain/migration and fencing | Deploy on a second host with safe stateful ownership; HA remains a separate design and recovery exercise |

Use vertical slices: design the smallest versioned contract, implement provider/API, expose the portal workflow, exercise real behavior, document limitations, then mark the item complete. Continue from the next implementation backlog above; the core F01/F03 slice is already delivered. Do not make portal SSO a prerequisite. Re-estimate work after the first custom application and full workload-restore drill; earlier engineering-day estimates are not current completion promises.

## Explicit deferrals and boundaries

- Portal OIDC/MFA/SSO and account-management redesign are deferred by user instruction. Preserve existing permission checks; do not turn “auth deferred” into anonymous management access.
- Multi-host/HA, autoscaling, advanced load balancing, virtual-network peering/VPN, IPv6, MIG/shared GPU, ARM64 and external cloud compatibility follow the single-host gates. Basic replica balancing and usable outbound access are included above.
- Existing Linux/desktop resources are containers sharing a kernel. True VMs, Windows guests and custom kernels require a separate hypervisor provider and are not part of the original container-based first release.
- Current storage is S3 plus managed local volumes. Azure Blob/File/Queue/Table APIs, SMB/NFS services, serverless functions and durable message queues are possible later catalog additions; they are not prerequisites for the explicitly requested initial set. Make supported protocols visible so a “storage account” label does not imply those APIs exist.
- Do not claim production readiness from the recorded development integration checks/groups. Their scope does not cover the missing workflows, storage exhaustion, complete recovery, GPU execution or dedicated-host qualification.

## Progress tracking for implementation

For each F-item record: state (`not started`, `in progress`, `implemented`, `qualified`, or `hardware blocked`), dependency status, changed components, migration/data impact, actual acceptance evidence and remaining limitations. This analysis closes no implementation gates. Delivered subfeatures are credited in the current capability inventory; use that evidence to avoid duplicating completed work.
