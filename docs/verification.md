# Development verification — October 27, 2026

This report records actual checks on the user's local Docker Desktop Linux engine. It does not certify production or hostile-tenant isolation.

## Managed application and data journey

The combined development pass completed **13 application-ingress groups**, **13 managed-binding groups**, **22 IPv4 policy groups**, **16 foundation groups**, **seven portal HTTPS/WSS checks**, Go tests/vet and the portal build. The generic application regression passed all 19 groups before checkpoint publication. Native SSH/desktop and the full shell suite remain earlier evidence; they were not repeated in this pass.

The example app performed actual PostgreSQL writes, signed S3 object round trips and Valkey operations through private bindings over CA-verified application HTTPS. The example-stack script completed twice with preserved resource IDs/data. The public API reference now records 51 paths; the local release manifest contains 25 current catalog/platform images. See [ingress progress](APPLICATION_INGRESS_PROGRESS.md) and [binding progress](APPLICATION_BINDINGS_PROGRESS.md) for scope and remaining gates.

## Reliability implementation qualification

The deployed reliability slice passed **22 IPv4 policy groups**, **11 operation-recovery groups**, **16 foundation groups** and **seven CA-verified HTTPS/WSS checks**. The final application regression passed **19 groups**, including verified restoration of the original demo image tag. See [RELIABILITY_PROGRESS.md](RELIABILITY_PROGRESS.md) for the detailed evidence. Go unit tests/vet passed. The private local release manifest records 24 current catalog/platform image identities.

The network additions verify repair of removed hooks/rules, flushed owned chains and hook priority, preservation of unrelated operator rules, and survival of an established TCP connection during unchanged checks. Operation tests deliberately supersede disposable create/restore attempts and verify renewal, stale-request rejection, continued policy observation, fresh recovery and no stale restore cleanup. This is a single-agent Docker Desktop qualification, with explicit daemon/in-flight and distributed fencing limits. Earlier shell/native/desktop results below were not rerun in this reliability pass. No portal source changed.

## Final resumed milestone

Subsequent application implementation: **19 application groups + three pull/connector/volume groups passed**. Regression results after those changes: **16 foundation, 28 shell, 17 IPv4 policy and seven HTTPS/WSS checks passed**, plus Go tests/vet and portal build. Metadata scratch restore passed with 106 resource records; the current catalog/platform manifest contains 24 image identities. Native SSH/desktop results below remain earlier evidence; they were not rerun in this application pass. See [APPLICATION_PROGRESS.md](APPLICATION_PROGRESS.md) for exact behavior and remaining gates.

| Verification | Result |
|---|---|
| Go unit tests and vet | Passed |
| Portal TypeScript and production build | Passed |
| Foundation integration | 16 groups passed |
| Browser shells, including all six SSH variants | 28 groups passed |
| Native SSH, bastion and network foundation | 14 groups passed |
| Browser desktop, including Origin and replay rejection | Six groups passed |
| IPv4 subnet policy, real disposable-resource traffic | 17 groups passed |
| CA-verified HTTPS and encrypted browser shell | Seven checks passed |
| Metadata backup and scratch restore | Passed, 83 resource records |
| Staged publication audit | No known installation secrets, private artifacts, private key blocks or personal machine paths found |
| Independent Gitleaks source scan | No leaks found; redacted report retained privately |

All reports, backup files and visual proofs are private under ignored `.local/`. The release manifest records 23 current image identities. Physical LAN/dedicated-host qualification, complete workload-volume recovery and the other release gates in the status document remain open. The earlier foundation observations below describe the same development environment and remain useful context.

## Reference environment

- Docker Engine: 29.4.0; Linux/amd64; Docker Desktop context.
- Engine-visible capacity: 28 logical CPUs and approximately 31.2 GiB RAM.
- Platform reservation: 2 vCPU and 6 GiB RAM.
- Node.js on development host: 24.14.1.
- Go builds/tests: pinned Go 1.26 Alpine container.
- Five platform services run: PostgreSQL metadata database, API, worker, restricted Docker agent, and portal.

## Passed automated integration checks

`node scripts/smoke.mjs` completed successfully with **16 check groups**:

1. Anonymous management access is denied.
2. Generated administrator credentials establish a session.
3. Real host capacity is returned; unqualified hard storage quotas are reported as unsupported.
4. A custom static website image serves a real local endpoint with the expected CPU/RAM runtime limits and ownership labels.
5. Identical deployment requests reuse a resource; changed bodies using the same key are rejected.
6. Managed PostgreSQL accepts a real table creation and write.
7. Managed Valkey authenticates and responds to PING.
8. Storage accepts AWS Signature V4 bucket/object operations, returns the written object, and rejects anonymous reads.
9. Workspace data survives stop/start and full container removal/recreation with its managed volume retained.
10. A reader in a second tenant cannot access another tenant's resources, logs, metrics, credentials, groups, or cost exports, and cannot create resources.
11. An oversized CPU allocation is rejected before provisioning.
12. Cross-origin management mutations are rejected.
13. Real Docker logs, current memory metrics, and allocation-based showback are available.
14. A deployment queued while the worker is stopped completes when the worker restarts.
15. Delete retains stateful data with storage showback; explicit purge removes the volume and ends that showback.
16. Two concurrent large CPU requests cannot overbook the host/subscription; exactly one is admitted.

Private machine-readable results are in `.local/smoke-report.json`. Test fixtures deliberately leave named example resources for inspection; historical deleted test records remain in activity/cost views.

## Other checks passed

The browser-shell addition has a separate execution record in [REMOTE_ACCESS_PROGRESS.md](REMOTE_ACCESS_PROGRESS.md). Its final dedicated suite passed **22 integration groups**, including real interactive shells in all six distro images, access grants and revocation, ticket expiry/replay, Origin rejection, non-root execution, Unicode, resize, Ctrl-C, persistent files, large output, stalled clients, session cleanup, natural exit status, logout, stop, and abrupt agent restart followed by recovery. Private results are in `.local/terminal-report.json`. A live portal shell was also exercised in the browser.

- `go test ./...`: encrypted credential round trip, rejection of tampering/wrong keys, rejection of invalid resource inputs and unapproved images, unqualified GPU rejection, and retained-storage cost behavior.
- `go vet ./...`: completed without findings.
- `npm run build`: TypeScript validation and Vite production build succeeded.
- Containerized platform build: API/worker/agent image and portal image built successfully.
- All six custom Linux workspace images were actually executed with a non-root UID; each wrote to `/data` successfully: Ubuntu, Debian, Alpine, Fedora, Rocky Linux, AlmaLinux.
- Platform database backup created successfully and restored into a scratch database with the same 11 resource records at the time of verification. The scratch database was removed afterward; the live platform database was not replaced.
- Development release manifest recorded 13 locally available image references/identities.
- Browser verification: signed in to the real portal, inspected the live dashboard and catalog, and deployed a custom HTML website named `portal-site` through the creation wizard.

## Limits and outstanding verification

The actual tested deployment is localhost HTTP on Docker Desktop. Hard per-resource storage quota enforcement, a prepared standalone Linux server, TLS/custom domains, WAF routing, network egress isolation, GPU execution, OIDC/MFA/RLS, full backup/restore of workloads, schema upgrades, image vulnerability/SBOM/signature qualification, and multiple hosts remain incomplete. The portal labels unavailable catalog capabilities accordingly.

Recovery tests prove queued-operation durability and retained-volume recreation. They do not yet prove recovery from every interrupted external operation, a physical disk failure, a host loss, or a network partition. The metadata backup check does not restore workloads or provide off-host protection.
