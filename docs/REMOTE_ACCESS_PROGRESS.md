# Remote access and networking implementation progress

Started: October 26, 2026. Scope: [remote-access plan](REMOTE_ACCESS_NETWORKING_PLAN.md).

**Development resumed at the user's request after the October 26 pause.** The retained installation was restored and the remote-access/networking implementation is now built. The expanded IPv4 suite passed 17 groups. Current results and remaining gates are recorded below.

This is the execution record. A phase is complete only when its stated acceptance evidence has been collected. Existing Docker Desktop workloads and data remain the development installation.

**Application phase regression:** after custom application deployment/update/retained restore and full catalog rebuild, the browser-shell suite passed 28 groups, IPv4 policy passed 17 groups and HTTPS/WSS passed seven checks again. Earlier workspace image approvals are now retained across full bootstrap. See [APPLICATION_PROGRESS.md](APPLICATION_PROGRESS.md); this does not complete the separate native RDP, physical LAN or production recovery gates.

**Functional planning update, October 27:** [CORE_FUNCTIONALITY_GAP_PLAN.md](CORE_FUNCTIONALITY_GAP_PLAN.md) now defines the next implementation sequence. Portal authentication upgrades are deferred by the user. Prioritize general application deployment, stable endpoints, day-two lifecycle and data recovery; reuse the remote-access foundation. F07/F08 capture remaining native RDP, network drift, egress and upgrade work. This update is analysis/documentation only and does not add new runtime test evidence.

## Current milestone — October 27, 2026

The resumed single-host remote-access milestone is built. Final checks passed: Go unit tests/vet, portal production build, 16 foundation integration groups, 28 browser-shell groups across six minimal and six SSH workspaces, 14 native SSH/network groups, six browser-desktop groups, 17 IPv4 policy groups, and seven CA-verified HTTPS/WSS checks. The desktop suite now includes foreign-Origin rejection and consumed-ticket replay rejection. SSH images remove inherited supplementary groups. Operator-owned image locks retain earlier approved workspace identities for existing resources.

The final metadata backup restored into a scratch database with 83 resource records. Workload volumes and recovery keys are excluded from that proof and need separate recovery procedures. The development release manifest records 23 current image identities. Bootstrap now builds the remote and policy images and pulls pinned guacd. OpenAPI documents 43 paths. Architecture choices, scope limits and vendor attribution are in ADR 0003.

Default access remains this PC at `http://localhost:8088`; optional loopback HTTPS is available at `https://localhost:8443`. No browser/OS trust store was changed. No physical LAN/public management exposure was enabled. Private configuration, credentials, SSH test keys, TLS keys, reports and backups stay under ignored installation paths. Repository publication uses a private GitHub repository after staged-source and commit-history audits.

Remaining release gates: dedicated Linux and physical LAN qualification; OIDC/MFA and restricted database roles/RLS; broader native RDP, permission, concurrency, fault/rollback and lifecycle testing; external firewall modification recovery; IPv6 policy; full workload backup/restore/upgrade; connector overhead accounting; disk quotas; WAF, GPU and broader master-plan services. Earlier pause notes below are historical and do not describe the current build.

| Phase | State | Evidence / remaining work |
|---|---|---|
| RA-01: architecture and proofs | In progress | Existing workspace shell verified as UID 1000 in `/data`; terminal architecture selected. Linux firewall and desktop privilege proofs remain. |
| RA-02: browser shell | Delivered: localhost development | Final 28-group real integration suite passed across six minimal and six SSH workspace variants, including output flow control, stalled client, natural exit, permissions, ticket expiry/replay, resize/Ctrl-C, revocation/logout, stop, and agent-restart cleanup/recovery. Wider release gates remain open. |
| RA-03: SSH images | Working development implementation | All six SSH variants built; real native SSH passes across all six. Public-key registration/binding/revocation, retained host identity and persistent homes implemented. Further negative and upgrade tests remain. |
| RA-04: networking foundation | Working development implementation | Actual private Docker subnets, IP reservations, DNS aliases, explicit TCP connectors and dependency guards implemented. Portal screens added; network enforcement is still labeled limited. |
| RA-05: native bastion | Working development implementation | Restricted gateway checks key/tenant/target/port authorization and rechecks every five seconds. Active-session revocation tested. Native RDP tunnel and broader concurrency tests pending. |
| RA-06: browser desktop | Working development implementation | Six protocol integration groups passed, including foreign-Origin and ticket-replay rejection. Visual portal verification passed: actual Xfce desktop, remote mouse interaction, terminal commands, UID 1000 and `/data/home/kzure`. Broader failure, isolation and lifecycle tests remain. |
| RA-07: enforced networking | Working IPv4 development implementation; 17 groups passed | Scoped adapter, serialized updates/rollback, pre-start enforcement, subnet associations and portal controls built and deployed. Seventeen real packet-test groups passed on disposable subnets, including management deny counters and cross-tenant rejection. Dedicated-host/physical-LAN qualification remains separate. |
| RA-08: remote release | Partial | Optional loopback HTTPS and CA-verified WSS tested. LAN/dedicated Linux, full recovery and production qualification remain open. Portal identity enhancements are deferred for the current functional plan. |

## Implementation decisions

- Browser shell uses the existing API and node agent; it requires no SSH daemon in the workspace.
- Owners and installation administrators can connect. Contributors need an explicit resource shell grant; readers cannot connect. Every active connection rechecks identity, access, resource state and generation every five seconds.
- Admission serializes session reservations: two per resource, five per user, 32 per installation. Pending tickets expire after 30 seconds; active leases expire after heartbeat loss.
- The platform injects its own statically compiled supervisor into the existing workspace. It runs as UID 1000, starts a child PTY, and controls resize/cleanup using a private Unix socket. It operates entirely inside the container.
- Metadata records connection/disconnection. Shell input and output are not stored. Byte output uses base64 transport to preserve UTF-8 across stream frame boundaries.
- The existing localhost publication remains the development access path. Remote LAN release depends on later HTTPS and isolation gates.

## Verification log

- Resume recovery: the selected Docker engine had no Kzure containers but retained database/workload volumes and images. Started the metadata database, saved a new private backup, and restored it into a scratch database with **49 resource records** before rebuilding the platform. The worker recreated the existing eleven active resources from their retained data.
- Resumed Go tests/vet and portal production build passed after reviewing and completing the draft policy adapter.
- Initial network enforcement suite: **14 packet-test groups passed** on a disposable IPv4 subnet. Verified actual same-subnet HTTP baseline, actual host listener baseline, default deny, retained Docker/operator rules, priority ordering, immutable host boundary, rejected malformed updates, connector-only access, connector revocation, stop/start, agent restart, absent IPv6 addressing/routes, and removal of the last association.
- Policy reconciliation now runs before provisioning protected resources and periodically thereafter. Updates are serialized; failed application restores the prior association/revision. Infrastructure attachments use the upper half of a private subnet; workload admission reserves addresses in the lower half and checks actual Docker allocations.

- Continued implementation after the user's instruction to complete the remaining track: six SSH images, bastion gateway, desktop image, network/IPAM/connectors, and portal controls added.
- `scripts/test-remote.mjs`: **14 groups passed**. Native SSH passed on Ubuntu, Debian, Alpine, Fedora, Rocky Linux and AlmaLinux. Tests also proved key-option rejection, overlapping-range rejection, host/home persistence through recreation, dependency protection, established-session revocation, connector DNS, actual RDP service startup, and honest network capability reporting.
- The bastion uses a small Go SSH forwarding server from the existing supported SSH library instead of an OpenSSH jump daemon. This allows target authorization and revocation to use Kzure's live API without exposing a shared shell. An architecture decision will record the change and its qualification limits.
- Browser desktop protocol suite: **4 groups passed**. The implementation uses the official Guacamole browser client and guacd, with a Kzure Go tunnel instead of a separate Java web/authentication application. The first TLS test exposed the FreeRDP fingerprint formatting requirement; correcting to colon-separated SHA-256 made pinned verification succeed without disabling certificate validation.
- Read-only host networking probe succeeded against Docker Desktop's Linux VM: iptables-nft backend, an existing DOCKER-USER chain, and bridge netfilter enabled. The controller must preserve the existing host/Docker rules. A scoped enforcement adapter and disposable packet-level tests are next; no packet enforcement has been claimed yet.
- Administrator password reset requested during implementation: added an operator-only reset CLI/script, revoked prior administrator browser sessions, saved a new private credentials file and verified sign-in. No credential reset endpoint was exposed over HTTP.
- Verified the reset administrator login through the actual portal as well as the API. Current credentials are in `.local/admin-credentials.txt`; passwords are not recorded in this document.
- Visual browser-desktop verification completed after the protocol tests: opened `desktop-lab` → Connect → Open browser desktop, saw the real Xfce desktop, launched its terminal, and executed `id` and `pwd`. The session reported UID 1000 and `/data/home/kzure`. Proof saved as `.local/screenshots/browser-desktop.png`. The desktop connection was then closed.

- Existing `linux-lab`: verified non-interactive shell reports UID/GID 1000 and working directory `/data`.
- Initial `go test ./...`: passed after adding terminal code.
- Portal TypeScript/Vite production build: passed. Terminal assets are loaded only when Shell is opened; final build has no oversized-chunk warning.
- First live integration run: 16 groups passed across all six distributions.
- Extended run: 19 groups passed, including expired tickets, reader denial and abrupt agent restart with orphan-job cleanup and subsequent recovery.
- Browser verification: opened `linux-lab` → Shell, entered commands, confirmed UID 1000 and `/data`, and expanded fullscreen.
- Added output acknowledgment after each consumed 16 KiB chunk, then began the final large-output/stalled-client regression run.
- Final terminal suite: **22 integration groups passed**, recorded at `2026-10-26T19:37:52.973Z` in `.local/terminal-report.json`. The test creates and purges disposable distro resources; scoped fixture users/tenants and historical audit rows remain.
- `go test ./...` and `go vet ./...` passed, including new Origin and terminal-size boundary tests. Platform and portal Docker images rebuilt and deployed successfully.
- Actual 15-minute idle and two-hour wall-clock durations have not been timed end-to-end; the implementation applies fixed limits. Dedicated-host networking, desktop privilege proofs, hostile-tenant qualification and remote HTTPS remain outstanding.
- Existing platform smoke suite rerun after the terminal release: **16 integration groups passed**, including real PostgreSQL/Valkey/S3 operations, retained data, queue recovery and concurrent quota admission.
- Final browser check after all recovery tests: `linux-lab` connected through the deployed portal, commands confirmed UID 1000 and `/data`, member selection controls rendered correctly, and no browser console errors were captured. Proof image: `.local/screenshots/browser-shell.png`.
- Post-change metadata backup restored successfully into a scratch database with 39 resource records. This includes the new schema but remains a metadata-only check; workload volumes and recovery keys are separate. Development release manifest refreshed with 13 local image identities.

## Delivered source changes

- `internal/platform/terminal.go`: scoped grants, hashed ticket admission, session history/termination, authenticated stream and ongoing authorization.
- `internal/platform/agent_exec.go`: private Docker exec bridge, image/generation checks, PTY input/output, resize, flow control and cleanup.
- `cmd/session/main.go`: unprivileged workspace PTY, private control socket, process-session cleanup and agent-loss lease.
- `apps/portal/src/BrowserShell.tsx`: terminal UI, reconnect/disconnect/fullscreen, session history, and member selection for access grants.
- Compose image build, portal WebSocket proxy/CSP, lazy terminal asset loading, backend CI build, OpenAPI and operator instructions updated.
- `scripts/test-terminal.mjs`: real Docker integration checks with disposable distro resources; private report written under `.local/`.
- `images/workspace-ssh/` and `scripts/access-images.mjs`: six SSH distro images with named user, persistent home and host identity.
- `internal/platform/remote*.go`, `bastion.go`, `agent_network.go`, `network.go`: key management, restricted native forwarding, target bindings, private Docker subnets/IPAM, DNS aliases and fixed-destination TCP connectors.
- `images/desktop/`, `internal/platform/desktop.go`, `agent_desktop.go`, and `apps/portal/src/BrowserDesktop.tsx`: actual Ubuntu/Xfce/xrdp desktop and authenticated Guacamole transport with certificate pinning.
- `apps/portal/src/RemoteAccess.tsx` and `NetworkPanel.tsx`: target/key controls, SSH configuration download, native RDP tunnel instructions, subnet selection and networking screens.
- `scripts/test-remote.mjs` and `scripts/test-desktop.mjs`: the passing 14-group native/network suite and 4-group desktop protocol suite.
- `internal/platform/admin.go` and `scripts/reset-admin.mjs`: operator-only administrator reset and sign-in verification.
- `internal/platform/network_policy.go`, `agent_policy.go`, policy registration/schema and `images/network-policy/`: completed and qualified for the documented single-host IPv4 development scope.

## Historical installation at the October 26 pause

- Portal: `http://localhost:8088`; administrator email: `admin@kzure.local`; current password: private `.local/admin-credentials.txt`.
- Working demonstration resources include `bastion-main`, `ssh-lab`, `desktop-lab`, and `network-client`, in addition to the existing platform examples and the user's `ubuntu-workspace`.
- `lab-vnet` / `workloads` uses a real `10.96.1.0/24` private Docker subnet and recorded address reservations.
- The native verification client's private key and strict SSH configuration are under `.local/ssh-tests/`. These are development test fixtures, not the user's personal SSH key.
- Existing base smoke/terminal results were collected earlier in the track. They have not been rerun against every latest remote-access change. A passing earlier report is not a final regression certification of the paused working tree.
- Running resources and data were preserved when development stopped. No shutdown or new build/test was performed as part of this pause.

## Historical resume checklist from the October 26 pause

1. Read this pause record and compare the working tree with the last deployed images. Inspect current resources and preserve private installation configuration and user data. Do not blindly rebuild or apply the draft firewall adapter.
2. Finish and review RA-07 before enabling it: integrate reconciliation and pre-start fail-closed behavior; validate generated rules/first-match priorities; fix empty protected-address handling in the adapter; validate rule/identity inputs; and qualify atomic apply, rollback, connection revocation and ownership-scoped cleanup. The latest policy files have not been compiled or tested.
3. Add the networking UI association/effective-policy controls and truthful capability reporting. Packet-test disposable resources for same-subnet, cross-tenant, host/management/LAN, published-port, IPv6 and restart/rollback behavior. Preserve pre-existing Docker/operator rules.
4. Complete the bastion/desktop lifecycle and failure matrix, native RDP tunnel verification, negative desktop permission/ticket cases, and whole-bundle resource-accounting checks. Review inherited Ubuntu supplementary groups and remove unnecessary memberships in future image revisions.
5. Complete install/release wiring: the main image bootstrap does not yet build/lock all new remote/policy images or include the pinned guacd dependency; the remote image scripts were invoked separately during development. Update OpenAPI, architecture decisions, README/status details and release manifests for the final implemented scope.
6. Rerun Go checks, portal build, base smoke tests, the terminal suite including SSH variants, native remote tests and desktop tests after the final code changes. Collect new reports; use the saved images only as prior visual evidence.
7. Continue RA-08: LAN HTTPS, federated/step-up identity, restricted database roles/RLS, fuller recovery/upgrade and release qualification. Dedicated Linux-host and physical-LAN qualification remains separate from successful Docker Desktop VM tests.

