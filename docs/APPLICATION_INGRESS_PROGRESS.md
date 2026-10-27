# Managed application ingress progress

Implemented October 27, 2026. Scope: the first hostname/TLS slice of F02 in [the core plan](CORE_FUNCTIONALITY_GAP_PLAN.md). Portal authentication upgrades remain deferred.

## Delivered behavior

- A shared, non-root Go application gateway runs in its own pinned Docker image with 0.5 vCPU / 128 MiB limits, all Linux capabilities dropped, a read-only root filesystem, bounded logs and no Docker socket or management credentials. It publishes HTTP 8089 and HTTPS 8444 on 127.0.0.1 only.
- Static sites and HTTP container applications have an Endpoint tab. Create a default resource-derived hostname or an available custom label under apps.kzure.localhost, open/copy the stable HTTPS address, inspect leaf certificate expiry, download the public CA, or remove the endpoint without deleting the app. Continuous workers and data/remote-access services cannot be routed by this workflow.
- Endpoint records survive application replacement, stop/start and retained-data restore. Invalid or duplicate names preserve working routes. Purge releases the selected hostname. Retained restore still reattaches existing data; it is not a backup.
- The gateway consumes a complete validated route snapshot with a content identity and atomically replaces its in-memory routing table. Invalid/partial files leave the prior table active. Changing a route cancels its old backend requests and clears idle pooled connections, preventing sockets on detached networks from lingering; unrelated active route lifetimes are preserved. Rolling replacement can briefly return 503 or disconnect an in-flight request. Unknown hosts return 404; withdrawn/unready backends return 503. HTTP redirects to the named HTTPS endpoint with path/query preserved. Standard HTTP streaming and WebSocket upgrades use Go's reverse proxy.
- Before changing a routed workload, the worker withdraws its route and waits for gateway acknowledgement. Only Running resources with a running, healthy (or no-health-check) container get an upstream. Stopped/starting containers with no network address withdraw only their route. Non-running routes release gateway network attachments, allowing dedicated workload networks to be removed safely. Unrelated resource provisioning does not depend on gateway availability.
- Gateway attachments are owned and limited to networks containing configured application targets. Associated IPv4 policy permits the gateway's actual subnet address to reach the declared target service port; explicit user deny rules still take precedence. Removing routes revokes the gateway path. Existing direct local workload ports remain available; WAF origin isolation is a later gate.
- The gateway creates an installation-local CA and wildcard leaf certificate in an owned persistent Docker volume. The leaf renews automatically near expiry, preserving the CA identity; gateway recreation reuses the same trust root. API/status responses expose only the public CA and expiry, never private key material. OS/browser trust settings are not modified.

## Using an endpoint

1. Open a Running static site or HTTP container app and choose **Endpoint**.
2. Optionally enter a unique lowercase application label, then choose **Create HTTPS endpoint**.
3. Download the application CA certificate and configure trust in the client that will access it. The application CA is separate from the optional portal HTTPS CA.
4. Open the HTTPS address on port 8444. If a client does not resolve the localhost subdomain, map that hostname to 127.0.0.1 in its hosts file. The native Windows/Node resolver on this development PC required explicit resolution in automated tests; automatic system DNS resolution is not claimed.
5. Updates briefly withdraw traffic while compute is replaced; the same URL returns when readiness succeeds. An unhealthy candidate is never enabled, and automatic application recovery can restore the previous configuration.

## Verification

The deployed ingress suite passed **13 groups**, including CA/hostname-verified HTTPS through a protected default-deny subnet, HTTP redirects, unknown-host/foreign-CA rejection, actual container restrictions, invalid/duplicate route preservation, independent static-site routing, healthy update, failed-candidate routing exclusion and automatic recovery, stop/start, retained restore, gateway recreation with preserved CA, selected-route purge, and concurrent route creation/purge without an orphan hostname. Tests use disposable resources and explicit loopback hostname resolution.

Go unit tests cover unsafe target/hostname rejection, complete snapshot identity, unknown/unready routing, redirect paths, and leaf renewal under the same trusted CA. Go tests/vet and the portal production build passed. The real portal created the demonstration application's endpoint at https://demo-api.apps.kzure.localhost:8444; a private UI capture is retained under the ignored screenshots directory. The combined pass also passed 13 application-binding groups, 22 IPv4 policy groups, 16 foundation groups and seven portal HTTPS/WSS checks. The core application regression passed all 19 groups for this checkpoint.

## Build, state and remaining gates

The normal image bootstrap now builds/locks kzure/ingress:0.1.0. The schema adds application_endpoints and ingress_status; existing resources and volumes are preserved. A verified metadata backup with 127 resource records was taken before the schema addition. Recovery must include the gateway data volume containing its CA/private keys as well as application data, platform metadata and image identities.

This qualifies loopback ingress on the development Docker Desktop Linux engine. Supplied certificates, arbitrary custom domains, physical LAN exposure, replicas/load balancing, no-bypass WAF attachment, private DNS management, root-CA rotation and wider Linux release qualification remain F02/F07/F10 work. The CA expires after three years and needs an operator rotation workflow before that gate is complete. Shared gateway overhead remains part of the reserved platform capacity until F12 exposes the full overhead pool. Do not mark all of F02 or M1 complete: managed application/data bindings are next.
