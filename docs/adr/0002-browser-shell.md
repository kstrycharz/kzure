# ADR 0002: Browser shell through scoped Docker exec

Date: October 26, 2026. State: implemented for localhost development; remote release remains gated.

## Context

Kzure's existing Linux workspace images contain no SSH server. Users need an interactive shell from the portal before SSH/bastion/networking providers are ready. Only the node agent has Docker access. Workspaces share a kernel, and all processes of one workspace user share that resource's security boundary.

## Decision

Use locally bundled xterm.js and an authenticated same-origin WebSocket. Authorize and reserve each session in PostgreSQL, then route through the existing private agent. The agent independently validates installation ownership, container generation, approved workspace image, Unix user and working directory.

The API issues a random single-use ticket with a 30-second lifetime. Store its hash and bind it to the requesting login session. Send it in the first WebSocket message, never the URL. Owners/admins can connect; contributors require an explicit resource grant; readers are excluded. Recheck the login, grant and resource every five seconds. Disconnect on stop/restart/deletion, logout, explicit termination or revocation.

Docker exec runs a statically compiled Go PTY supervisor as UID/GID 1000. The platform copies this helper into `/tmp`; it does not change the workspace image or persistent user data. A private Unix socket permits resize, stop and heartbeat requests. The shell starts a separate Unix session; cleanup signals only processes in that session. A local 25-second lease plus a five-second watchdog cleans up after abrupt agent loss even if Docker retains exec stdin. Users can intentionally launch detached processes outside a terminal session; resource limits still apply to those processes.

Output is transported as base64 bytes to avoid UTF-8 corruption at Docker frame boundaries. The agent sends at most 16 KiB of decoded output, then waits for an acknowledgment sent after xterm finishes consuming that chunk. A missing acknowledgment closes the stream after ten seconds. Input messages and socket buffers are bounded, with write deadlines to disconnect stalled clients. Metadata is audited; terminal contents are not recorded. OSC clipboard writes are suppressed. Browser assets are local and the portal sets a content-security policy.

## Consequences

No per-workspace SSH exposure is needed. Existing approved workspace images gain browser access without recreation. The helper requires a Linux amd64/arm64 build matching its platform image and an image with `/bin/sh`, `/data`, writable `/tmp`, and UID 1000; approved images are tested individually. No claim of hostile-user isolation inside the same workspace is made.

Interactive sessions are ephemeral. Reconnect starts a new shell. Two-hour duration and 15-minute input idle limits are fixed development defaults. Admission caps are two sessions per resource, five per user, 32 per installation. Later configuration must preserve hard installation ceilings.

The agent currently invokes a short Docker exec for each resize/heartbeat; measure overhead before raising concurrency. An agent upgrade disconnects active shells; the portal offers Reconnect. Remote LAN access still requires HTTPS, identity hardening, and qualified network isolation. Dedicated Linux network/desktop architecture proofs remain part of RA-01.
