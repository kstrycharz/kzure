# Remote access and single-host IPv4 policy

Status: implemented development preview, October 27, 2026.

## Decisions

Native access uses a small Go SSH forwarding gateway. It accepts only `direct-tcpip` channels for explicitly assigned resource aliases and ports 22/3389. It has no shared shell. Each authorization consults the control plane using a separate per-bastion credential; active grants are checked every five seconds. Ed25519 host identities persist in encrypted platform metadata and workspace data volumes. Clients must verify the displayed fingerprints. Workspace SSH uses the non-root `kzure` account with persistent home under `/data/home/kzure`; passwords, root login and forwarding are disabled.

Browser desktop uses the Apache Guacamole protocol and a pinned official guacd 1.6.0 sidecar. The sidecar binds only to its private transport interface and receives RDP credentials from the agent. The agent pins the retained desktop certificate. Browser tickets are short lived, single use and bound to the authenticated cookie. Clipboard, files, drives and audio are disabled. The native gateway and guacd share the bastion's CPU/RAM allocation; fixed service connectors currently count as platform overhead and still need full accounting.

Private subnets are internal Docker bridges with static resource IP reservations. Workloads use the lower address pool; infrastructure attachments use the upper pool after checking actual Docker occupancy. Fixed-destination connectors provide explicit service paths and DNS aliases. They do not provide general routing or peering.

An associated security group compiles to installation-owned IPv4 iptables chains inside the Linux Docker engine. Priorities are first-match and unmatched traffic is denied. Platform host, management, transport and protected tenant boundaries precede user rules. Authorized bastion and connector paths are narrow exceptions. A helper with NET_ADMIN applies one generation atomically and invalidates conntrack only for protected resource addresses. It does not mount the Docker socket. Existing Docker and operator rules are retained. The worker applies policy before protected resource creation and reconciles after restart; failed changes restore the preceding association.

## Evidence and limits

Seventeen disposable-resource packet checks passed in the Docker Desktop Linux VM, including management deny counters, stateful connector traffic, priority, revocation, same-subnet denial, host boundaries and restart recovery. Cross-tenant traffic was blocked and the explicit platform deny was installed; Docker can reject those packets before the Kzure chain, so that test does not attribute the drop exclusively to Kzure. No Docker isolation rule was removed for testing.

This is single-host IPv4 qualification. Physical LAN, dedicated Linux, hostile privileged workloads, externally modified firewall rules, broader published-port and rollback fault injection, IPv6 policy, OIDC/MFA, database RLS and full workload recovery remain release gates. Unassociated subnets do not acquire security-group default-deny behavior. The Docker agent is a privileged trust boundary and the portal is intended for a trusted local operator.

Optional local HTTPS listens on loopback port 8443. Its CA and private keys stay under ignored `.local/tls`; no trust store is changed automatically. It preserves the default localhost HTTP portal. LAN and public ingress require a separate qualified deployment configuration.

## Third-party browser client

`apps/portal/public/vendor/guacamole-common.js` is Apache Guacamole's bundled browser client, extracted from the official `guacamole/guacamole:1.6.0` image at digest `sha256:f344085e618bb05e22b964b0208dbd06d3468275bac70206f93805245e067b40`. The vendor directory includes its Apache license and notice. The server side uses `guacamole/guacd` at the immutable digest recorded in the image bootstrap. Assets are served locally under the portal's content policy.
