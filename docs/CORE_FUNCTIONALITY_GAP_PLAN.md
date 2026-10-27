# Kzure core functionality gap analysis and delivery plan

Reviewed: October 27, 2026. Analysis baseline: commit `85290f2`. Subsequent implementation is recorded below; the original inventory/source findings describe that baseline.

## Implementation update — October 27

See [APPLICATION_PROGRESS.md](APPLICATION_PROGRESS.md) for current behavior, operator steps and qualification limits. The original review below was documentation-only; subsequent development has added actual implementation and tests.

| Work | Current state | Evidence / remaining work |
|---|---|---|
| F01 custom applications | Core implementation qualified for this development host | 19 application groups and three additional pull/network/volume groups passed. Local/public image registration, architecture checks, retained immutable image tags, HTTP service/worker modes, config, encrypted secret references, health commands and one persistent mount work. Private registries, image archive import and finite jobs remain. |
| F02 endpoints | Initial local port identity implemented | Reserved application ports survive recreation, update and retained restore. Hostname/application TLS ingress and replica routing remain. |
| F03 lifecycle | Application revisions implemented | Image/configuration changes, CPU/RAM resize, conditional edits, automatic recovery of failed health checks and explicit rollback passed. Brief compute interruption is expected. Other providers, storage growth/migration, zero-downtime rollout and full drift reconciliation remain. |
| F04 recovery | Existing retained-volume restore implemented | Data/credential/endpoint retention and missing-volume refusal passed. Provider-aware backups, restore from backup and full installation/workload recovery remain. |
| F08 reliability/upgrade | Supporting fixes implemented | Failure writes check the operation attempt; full image bootstrap retains workspace approval history. Full agent side-effect fencing, concurrency/fault qualification, retry/backoff/cancel and complete registered-image release export remain. |

M1 is still in progress: complete managed application ingress and a verified custom app + database/storage binding journey. M2/M3 work has not been marked complete by these changes. Portal authentication enhancements stay deferred. Treat the following assessment/inventory/source findings as the original review, and use this table for current implementation status.

This is the current execution plan for completing the single-host product. It supplements the original [project specification](../PROJECT_PLAN.md) and replaces its old sequential priorities where they conflict. This review inspected source, API routes, schemas, provisioning/reconciliation, portal flows, build/backup scripts and recorded verification. It did not run new workload tests or change the deployment. Earlier passing tests remain evidence only for their tested behavior.

**Portal authentication is deferred at the user's request.** OIDC, MFA, SSO and login/account-management redesign are outside this execution sequence. Existing checks and secret handling remain in place. Workload credentials, network boundaries, backup safety and capacity limits are still functional requirements for running local services.

## Assessment

Kzure is a working local resource manager with unusually complete remote-access foundations. It is not yet a general-purpose application platform: the biggest missing journey is deploying an arbitrary operator-approved application, configuring it, giving it a stable address, updating it and recovering its data from the portal.

Do not spend the next milestone adding more distro cards or rebuilding the completed shell/bastion features. Complete the application and data lifecycle first. Container apps, stable ingress, retained-data recovery and update/resize are more immediately useful than additional platform branding or portal login features.

## Capability inventory

| Area | Implemented today | Core gap and practical consequence | Work |
|---|---|---|---|
| Organization | Tenants, subscriptions, resource groups, resource creation and lists | No quota-editing workflow, tags, deletion locks, dependency-aware group deletion or reusable deployment templates | F09 |
| Container apps | Fixed approved Nginx template with CPU/RAM limits | Cannot deploy your own image, set application environment/secret references, command, exposed ports, probes or persistent mounts | F01 |
| Application networking | Allocated localhost ports; private aliases/connectors | Application addresses can change after recreation. No general managed app hostname/TLS routing, replica balancing or explicit endpoint exposure model. Portal HTTPS does not provide application HTTPS | F02 |
| Lifecycle | Create, start, stop, restart, retain/delete and purge | No editable spec, resize, revision history, safe update, rollback, clone or restore action. Retained data can only be purged through the current API | F03, F04 |
| Static web | Single HTML file or welcome page | Cannot deploy a normal site containing CSS, JS, images and routes; no archive deployment, update history or rollback | F06 |
| Storage | Dedicated real S3 service and persistent Docker volumes | No object browser, reusable managed-volume resource, physical usage admission, enforced disk quotas or workload backup/restore | F04, F05, F06 |
| Managed databases/cache | Dedicated PostgreSQL and Valkey with generated credentials | No database-aware recovery, version upgrade workflow, configuration management, service credential rotation or app binding that supplies connection settings | F04, F06 |
| Private networking | VNet/subnet records, actual bridges, IP reservations, connectors, associated IPv4 rules | No managed outbound-access policy/NAT workflow, private DNS records/zones or topology diagnostics. Firewall drift is not independently checked when the desired hash is unchanged | F07 |
| Shell/SSH/desktop | Working browser shell, six SSH distros, bastion, browser desktop | Remaining qualification and usability work: native RDP client journey, reconnect/failure matrix, readable effective-access diagnostics and image-upgrade continuity | F07, F08 |
| Operations | Durable operation rows, leases, polling, missing-container recovery | No step-level retry/cancel/progress or full spec-drift reconciliation. A slow operation blocks the single worker loop's normal reconciliation. Health history/alerts are absent | F08 |
| Costs/capacity | Fixed CPU/RAM/storage estimates, allocation intervals, current metrics, CSV | No editable rates, date-range analysis, budgets, GPU allocation costs, complete infrastructure overhead or outage uncertainty | F12 |
| WAF | Unavailable catalog entry | No deployable WAF policy/gateway, request inspection or protected origin path | F10 |
| CPU/GPU compute | Long-running Linux workspaces | No finite batch/scheduled jobs, GPU inventory, exclusive device allocation, actual GPU execution or GPU metrics | F11 |
| PaaS/source builds | Unavailable app-service entry | No local registry workflow, isolated source builder, runtime recipes, build logs or source-to-revision deployment | F01, F06 |
| Installation/recovery | Compose bootstrap, image builds/locks, metadata scratch restore, image export script | No clean-host acceptance, supported upgrade/migration workflow or complete installation/workload restore drill | F04, F08 |

## Specific source findings to address

These are confirmed implementation limitations unless explicitly described as a risk requiring a reproduction test.

1. **The generic container resource is a template.** `Catalog` in [model.go](../internal/platform/model.go) fixes `container-app` to Nginx; `validateSpec` rejects a different image. `Spec` has only CPU, RAM, storage, HTML, image and subnet fields. F01 must expand the supported schema and execution path, not merely reveal an image text box.
2. **Retain currently has no product recovery path.** `resourceAction` in [api.go](../internal/platform/api.go) rejects every action except purge for `Retained`. Add a first-class restore/reattach workflow before presenting retained data as readily recoverable.
3. **The backup proof is metadata-only.** [backup.mjs](../scripts/backup.mjs) explicitly excludes workload volumes, images and recovery keys; its verification compares resource counts after restoring the database. This does not prove recovery of PostgreSQL application rows, S3 objects, cache data or workspace files after disk loss.
4. **Application identity and routing are incomplete.** [agent.go](../internal/platform/agent.go) requests an empty host port for published services, so a recreated container may receive a different port. Its `ensure` method starts an existing container without reconciling its image/spec. There is no immutable deployed revision recorded per resource.
5. **Storage admission is declarative.** [api.go](../internal/platform/api.go) checks requested subscription storage; the host response reports CPU/RAM and fixed reservations, not data-pool free bytes/inodes or physical storage headroom. No disk-limit enforcement is implemented.
6. **Recovery needs explicit failure semantics.** [worker.go](../internal/platform/worker.go) reclaims expired operation leases but marks agent errors Failed without classified retry/backoff; its periodic resource scan excludes Failed resources. Its failure branch conditionally updates the operation attempt but updates resource failure state without the success branch's attempt check. Add a delayed stale-worker failure test and fence all writes before claiming concurrent-worker safety.
7. **Firewall application status can become stale.** [agent_policy.go](../internal/platform/agent_policy.go) returns early when the desired generation matches its in-memory hash. A host tool removing a hook may therefore escape detection until a changed generation or agent restart. Inspect actual owned hooks and reconcile observed state.
8. **Full image bootstrap loses retained approval history.** [images.mjs](../scripts/images.mjs) starts a fresh lock map. The narrower SSH builder retains old identities, but the full bootstrap can discard them. Existing shells check approved image identities; test and fix full-bootstrap upgrade continuity, including minimal workspaces, before relying on that history feature.
9. **Day-two management and diagnostics are thin.** The [API](../internal/platform/api.go) exposes create/list for hierarchy objects but no quota update, PATCH/ETag, deployment preview or backup/restore route. The portal shows current Docker metrics and bounded logs, not historical utilization or alerts. [NetworkPanel.tsx](../apps/portal/src/NetworkPanel.tsx) requires JSON rules instead of a normal rule editor.
10. **Some status text is stale.** The old project header said nothing had been built, the status page said work was paused and firewall source unfinished, and current tables still used earlier 22-shell/four-desktop counts. This documentation pass corrects those statements. Portal copy that still says SSH/desktop is “coming next” belongs in F09.

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
| M1 — Host my own application | F08 fencing/upgrade continuity baseline → F01 custom images/config → F02 stable endpoints → F03 update/resize; begin F04 retained recovery and F12 current cost accuracy alongside these | Deploy a custom app + database with persistent data, stable URL, safe update/rollback, correct capacity/cost and a usable retained-data recovery path |
| M2 — Keep and recover real workloads | F04 workload backups/restore → F05 disk safety → F07 operational networking → F09 management/templates; finish F06 static/object/data UI and F08 monitoring/recovery | A normal operator can deploy, diagnose, back up and restore a useful multi-resource stack through supported UI/API workflows; complete a clean-host restore drill |
| M3 — Complete requested single-host catalog | F06 source-build PaaS → F10 WAF → F11 CPU/GPU jobs; complete F12 rate/budget/GPU/overhead analysis and Linux release qualification | Every originally requested service family is backed by working local resources and actual acceptance evidence; explicitly record any hardware-dependent blocked gate |
| M4 — Multi-host expansion | Node inventory/enrollment, placement, cross-host networking, volume locality, drain/migration and fencing | Deploy on a second host with safe stateful ownership; HA remains a separate design and recovery exercise |

Use vertical slices: design the smallest versioned contract, implement provider/API, expose the portal workflow, exercise real behavior, document limitations, then mark the item complete. Start with F01 and the minimum F03/F08 foundation it needs. Do not make portal SSO a prerequisite. Re-estimate work after the first custom application and full workload-restore drill; earlier engineering-day estimates are not current completion promises.

## Explicit deferrals and boundaries

- Portal OIDC/MFA/SSO and account-management redesign are deferred by user instruction. Preserve existing permission checks; do not turn “auth deferred” into anonymous management access.
- Multi-host/HA, autoscaling, advanced load balancing, virtual-network peering/VPN, IPv6, MIG/shared GPU, ARM64 and external cloud compatibility follow the single-host gates. Basic replica balancing and usable outbound access are included above.
- Existing Linux/desktop resources are containers sharing a kernel. True VMs, Windows guests and custom kernels require a separate hypervisor provider and are not part of the original container-based first release.
- Current storage is S3 plus managed local volumes. Azure Blob/File/Queue/Table APIs, SMB/NFS services, serverless functions and durable message queues are possible later catalog additions; they are not prerequisites for the explicitly requested initial set. Make supported protocols visible so a “storage account” label does not imply those APIs exist.
- Do not claim production readiness from passing the current 88 integration checks/groups. Their scope does not cover the missing workflows, storage exhaustion, complete recovery, GPU execution or dedicated-host qualification.

## Progress tracking for implementation

For each F-item record: state (`not started`, `in progress`, `implemented`, `qualified`, or `hardware blocked`), dependency status, changed components, migration/data impact, actual acceptance evidence and remaining limitations. None of the new F-items is marked completed by this analysis. Existing subfeatures are credited in the capability inventory; use that evidence to avoid duplicating completed work.
