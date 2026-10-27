# Managed application data bindings

Implemented October 27, 2026. Scope: slice 3 of [the execution plan](CORE_FUNCTIONALITY_GAP_PLAN.md), supporting F01/F03/F06. Portal authentication enhancements remain deferred.

## Delivered behavior

- The Bindings tab connects a running container application or continuous worker to a same-tenant PostgreSQL, Valkey or S3 resource. The target must be running when a new binding is created. Choose a distinct uppercase environment prefix or use DATABASE, CACHE or STORAGE defaults.
- A binding supplies a stable private connector alias and validated environment contract. Public connection settings are listed in the portal. Passwords, connection URLs containing credentials and S3 keys are encrypted in immutable binding snapshots and deployment/revision snapshots; specifications contain binding IDs. Metadata lists expose secret variable names, not their values. Existing owner checks apply to credential binding.
- PostgreSQL receives HOST, PORT, DATABASE, USER, PASSWORD and URL under its prefix. Valkey receives HOST, PORT, PASSWORD and URL. S3 receives ENDPOINT, REGION, FORCE_PATH_STYLE, ACCESS_KEY_ID and SECRET_ACCESS_KEY. The example app uses the default prefixes; custom applications can use their own prefix contract.
- Adding/removing bindings uses the same stale-edit protection, resource admission, encrypted deployment snapshot, readiness and rollback workflow as ordinary configuration changes. Conflicting variable/prefix definitions, wrong-application IDs, stale revisions and cross-tenant targets are rejected.
- Binding connectors are prepared before an application replacement when possible. Stopped/missing compute retains connection metadata for reconciliation when it starts. Active bindings protect their connectors from manual removal; normal dependency checks protect target deletion.
- Unbinding removes injected settings through a new revision. An owned connector is removed after the binding is no longer required by the current configuration or an in-progress replacement's previous revision. Pre-existing operator-created connectors are preserved. Immutable previous binding snapshots support rollback and recreation of their saved connector identity.
- Secret values are supplied to the application process and remain visible to the Docker host operator. This preserves the existing trusted single-host boundary; it is not isolation from a host administrator. Resource logs redact current/recent secret snapshot values.

## Portal workflow

1. Create the PostgreSQL/cache/storage resource and the application.
2. Open the running application and choose **Bindings**.
3. Select its data service and optional environment prefix, then **Connect service**. Follow Activity until the application returns to Running before adding another binding.
4. Configure the application to read the displayed variable names. Use the Endpoint tab for its managed HTTPS URL.
5. Remove a binding and wait for its configuration revision before deleting its managed connector or target service. Removing an adopted binding leaves the operator-created connector for an explicit separate action.

The example Node image includes a /bindings route that writes a PostgreSQL row, performs a signed S3 object round trip and increments a Valkey counter. It returns results without credential values. Its PostgreSQL client comes from the pinned Alpine-based image build; the final image identity is locked after build.

## Verification

The final suite passed **13 live groups**: actual app-originated database writes, secret-free metadata/specs, dependency protection, overlapping-prefix refusal, signed S3/cache operations, CA-verified app HTTPS, application recreation with persistent database content, unbinding/connector revocation, rollback/connector recreation, encrypted snapshots/log protection, stopped configuration, stale/cross-application/cross-tenant checks and operator-connector adoption.

Go unit tests cover private endpoint/prefix validation, URI escaping and separation of ordinary settings from credential values. Go tests/vet and the portal production build passed. Private fixture reports and the example-stack manifest remain in ignored local paths. The example-stack script completed twice with the same resources and preserved data; the real portal displays all three bindings as Ready with secret values hidden.

## Remaining scope

Credential rotation of data providers and coordinated binding refresh, source-build PaaS, provider configuration/upgrades and backup recovery remain F04/F06 work. A rollback reuses its saved credential snapshot, so it needs the target service and those credentials to remain valid; it does not undo database migrations. Remove bindings before retaining/purging a bound application under the current dependency rules, and restore a prior applied configuration to reattach saved bindings afterward. Automated dependency-aware group teardown is F09 work.

Binding history is retained for supported revision rollback; retention/pruning policy and full provider recovery remain open. CPU/RAM overhead for connectors remains within the reserved platform pool until F12 exposes complete infrastructure showback. This delivers the application/data connection journey without claiming all of F06 or the complete product is finished.
