# ADR 0001: A local development control plane with a separate Docker agent

Date: 2026-10-26. Status: Accepted for the development preview.

## Context

The available host is Windows with a working Docker Desktop Linux engine, 28 logical CPUs and approximately 31.2 GiB of engine-visible memory. The user confirmed one host first, expansion later. Go is not installed on Windows, so builds and backend verification run inside a pinned Go container.

The master plan describes production acceptance gates that require a prepared Linux filesystem, TLS/domain setup, identity federation, backup storage, and optional GPU hardware. Those have not been qualified here. Useful implementation can continue with these features clearly unavailable.

## Decisions

- Keep the planned Go, React/TypeScript, PostgreSQL, and Docker architecture. Separate API, worker, and privileged node agent processes/containers.
- Use a small typed adapter over the Docker Engine HTTP API v1.47 instead of adding the moving Docker SDK module surface to this first milestone. Only allow operations derived from approved catalog definitions and resource identities. Current engine minimum/API compatibility was inspected on the actual host.
- Bootstrap local bcrypt-authenticated users, hashed session tokens, and tenant membership checks. OIDC/Keycloak, fine-grained subscription roles, PostgreSQL RLS with non-owner DB roles, and MFA are explicit follow-on work. The current API/worker database user is privileged within the platform database and is not a completed RLS boundary.
- Bind the development portal to localhost over HTTP, with HttpOnly/SameSite Strict cookies, strict mutation origins, and a required custom request header. Workloads bind to 127.0.0.1, a different cookie hostname. Local TLS and LAN operation require the planned gateway phase. This preview is not a publicly exposed identity system.
- Communicate between platform services over a Docker-internal network using a strong installation token for the node agent. Cross-host mTLS is deferred until host enrollment is implemented; internal HTTP is not claimed to be encrypted.
- Give each workload its own network. Do not claim this implements the full host/LAN egress policy: Docker/host packet-level isolation qualification is pending.
- Use Docker named volumes as a development storage adapter and report hard quotas as unsupported. Never format disks to satisfy a gate on the user's Windows machine.
- Ship dedicated storage accounts using a pinned SeaweedFS wrapper configured to grow one volume at a time. The upstream seven-volume growth default exceeded the original five-volume development setting and was caught by a real object-upload test. Use smaller 1 GiB internal volume size with a bounded volume count. These internal settings are not a per-account filesystem hard quota.
- Build six non-root workspace images from pinned distribution bases. Workspaces expose persistent `/data`; browser terminal and managed home directories are later features.
- Keep rates fixed in the preview and store numeric allocation intervals. The editable historical rate-card model remains future work. Do not expose an unimplemented GPU cost meter as if a GPU were allocated.

## Consequences

This milestone exercises the most important resource lifecycle through real Docker services and keeps the portal honest about limitations. It is a development foundation, not completion of the plan's R1/R2 security, quota, TLS, recovery, or release gates. Expand in vertical slices, preserving installation/resource labels, idempotency, ownership checks, durable state, and scoped API authorization.
