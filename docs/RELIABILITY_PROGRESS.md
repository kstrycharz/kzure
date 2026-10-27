# Network and operation recovery progress

Started October 27, 2026. Scope: slice 1 in [the current execution plan](CORE_FUNCTIONALITY_GAP_PLAN.md), supporting F07/F08. Portal authentication enhancements remain deferred.

## Implemented behavior

- The agent no longer treats an unchanged desired firewall generation as evidence of enforcement. Each policy reconciliation launches the pinned network adapter, which compares canonical observed owned chains/rule order and parent-hook positions against the fingerprint recorded after the previous successful application.
- Missing hooks, removed generation rules, flushed previously observed stable chains and moved/duplicated hooks trigger owned-policy reapplication. Unrelated Docker/operator rules are preserved. Foreign nonempty stable-chain content is rejected rather than overwritten. Counters and unrelated rules are excluded from the fingerprint.
- A healthy unchanged observation skips rule replacement and connection-tracking cleanup. Changed or repaired policy clears tracked state only for protected addresses, preserving the existing revocation behavior. The recorded fingerprint comes from successful adapter output; missing/malformed evidence fails the apply.
- Resource/remote/connector maintenance and policy reconciliation run independently of the single provisioning loop. Each maintenance loop remains serial; slow provisioning no longer holds up periodic firewall observation. Existing network policy locking still serializes configuration changes.
- Running operation leases renew every 15 seconds, conditional on the same running, unexpired attempt. Loss of ownership or database contact cancels dispatch/readiness work. Initial resource transitions and terminal database writes validate the current lease under an operation-row lock.
- Lifecycle calls carry operation ID/attempt metadata. The agent serializes lifecycle mutations per resource and checks the durable operation ledger through an internal token-protected API before accepting a request and before each guarded Docker mutation/container-file write. The check includes resource identity, attempt, Running state, expiry and allowed lifecycle action. Stale, completed or mismatched claims are rejected, including after agent restart.
- Create/start/restore can dispatch ensure, update can dispatch replacement, and restore can dispatch compute deletion for failed-restore cleanup. Only a purge operation can authorize purge. Existing API owner checks still govern who can queue that operation.
- Lost restore attempts skip cleanup and result writes. Setup cleanup retains the operation check even when the request context has been canceled, preventing background cleanup from bypassing ownership validation. Checks fail closed if the management API is unavailable.

## Verification

The final network pass passed **22 IPv4 policy groups**, including a real established TCP connection surviving unchanged observations. The final operation-recovery suite passed **11 groups**, including strengthened delayed-ensure checks. Foundation regression passed **16 groups**, CA-verified HTTPS/WSS passed **seven checks**, and the final application regression passed **19 groups**. Go unit tests/vet passed after the final source changes. The application suite restored the original demonstration image tag to its catalog identity; the 24-image release manifest was regenerated successfully.

The operation suite deliberately supersedes disposable create and restore attempts while readiness is pending. It verifies actual containers and retained volumes remain available, stale results do not overwrite the replacement attempt, delayed requests are rejected, a fresh operation can recover the resource, policy observation continues, and agent restart does not erase operation ownership checks. Fixture credentials and reports stay under ignored local paths. Publication checks scanned 124 indexed files for installation-private values and private artifacts, and the independent Gitleaks source scan found no leaks.

Run the focused suites sequentially because they restart the agent and deliberately alter disposable operation records:

~~~sh
node scripts/test-network-policy.mjs
node scripts/test-operation-recovery.mjs
node scripts/test-applications.mjs
~~~

## Deployment and data impact

There is no database schema or workload-volume migration. Rebuild the network-policy image, update its existing image lock through the normal image bootstrap, and deploy matching API/worker/agent images. Old workers do not send operation metadata and cannot drive the new guarded lifecycle endpoints; quiesce queued/running operations before a coordinated upgrade. This is a compatibility requirement, not a completed general upgrade/drain workflow.

The default internal ownership-check URL is http://api:8090; KZURE_CONTROL_URL can configure an equivalent trusted management endpoint. The agent still has no database credentials. The catalog image lock and local 24-image release manifest were refreshed after rebuilding the adapter and default demonstration image. Application test fixtures now preserve and restore the original demo tag so test version changes do not invalidate the catalog lock. No additional host bind path, public management listener or Docker socket consumer is introduced.

## Remaining limits and next steps

This qualifies one agent controlling one Docker Desktop Linux engine. The ownership check and a Docker daemon mutation are separate requests: a daemon operation already accepted cannot be atomically revoked by changing the metadata lease. Per-resource serialization is local to the agent process, not a distributed lock. Full daemon restart/in-flight ambiguity, multiple agents, complete side-effect journaling and multi-host fencing remain F08 work; do not claim distributed exactly-once execution.

Periodic repair restores observed enforcement after a delay; it does not prevent a host administrator from changing rules between checks. Reconciliation normally waits 20 seconds between completed passes, with additional bounded adapter/lock time. A blocked policy operation can delay its own serial loop. Physical LAN, dedicated Linux and IPv6 remain separate qualification gates. Unassociated subnets still have no Kzure security-group enforcement.

Retry/backoff/cancel UI, operation step progress, full spec-drift reconciliation, health history/alerts and a clean supported-Linux upgrade/recovery drill remain open. The next application milestone is managed hostname/TLS ingress, followed by database/S3 binding workflows; workload backup design proceeds alongside it. See F02/F04/F06 and the [master plan](../PROJECT_PLAN.md).
