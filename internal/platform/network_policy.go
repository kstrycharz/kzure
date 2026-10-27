package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const policySchema = `
CREATE TABLE IF NOT EXISTS subnet_security_groups(subnet_id text PRIMARY KEY REFERENCES subnets(id),security_group_id text NOT NULL REFERENCES network_security_groups(id));
CREATE TABLE IF NOT EXISTS network_policy_status(id int PRIMARY KEY CHECK(id=1),state text NOT NULL DEFAULT 'Not configured',generation text NOT NULL DEFAULT '',error text NOT NULL DEFAULT '',updated_at timestamptz NOT NULL DEFAULT now());
INSERT INTO network_policy_status(id) VALUES(1) ON CONFLICT DO NOTHING;
`

type PolicyResource struct {
	ID       string        `json:"id"`
	TenantID string        `json:"tenantId"`
	SubnetID string        `json:"subnetId"`
	Address  string        `json:"address"`
	CIDR     string        `json:"cidr"`
	Rules    []NetworkRule `json:"rules"`
}
type PolicyLink struct {
	SourceID    string `json:"sourceId"`
	TargetID    string `json:"targetId"`
	ConnectorID string `json:"connectorId,omitempty"`
	Port        int    `json:"port"`
}
type PolicyRequest struct {
	Resources []PolicyResource `json:"resources"`
	Links     []PolicyLink     `json:"links"`
	Ingress   []string         `json:"ingress,omitempty"`
}
type policyEndpoint struct {
	PolicyResource
	AllowsIn  []string
	AllowsOut []string
}

func (a *App) networkPolicyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/subnets/{id}/security-group", a.handle(a.attachSecurityGroup))
	mux.HandleFunc("DELETE /api/subnets/{id}/security-group", a.handle(a.attachSecurityGroup))
}

func (a *App) attachSecurityGroup(w http.ResponseWriter, r *http.Request) error {
	return a.withPolicyLock(func() error { return a.attachSecurityGroupLocked(w, r) })
}

func (a *App) withPolicyLock(fn func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	conn, e := a.DB.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	if _, e = conn.ExecContext(ctx, "SELECT pg_advisory_lock(7392105)"); e != nil {
		return e
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(7392105)")
	return fn()
}

func (a *App) attachSecurityGroupLocked(w http.ResponseWriter, r *http.Request) error {
	sid := r.PathValue("id")
	var tid string
	if e := a.DB.QueryRow("SELECT tenant_id FROM subnets WHERE id=$1", sid).Scan(&tid); e != nil {
		return e
	}
	if e := a.owner(r, Resource{TenantID: tid}); e != nil {
		return e
	}
	var in struct {
		SecurityGroupID string `json:"securityGroupId"`
	}
	if r.Method != "DELETE" {
		if e := decode(r, &in); e != nil {
			return e
		}
		var groupTenant string
		if e := a.DB.QueryRow("SELECT tenant_id FROM network_security_groups WHERE id=$1", in.SecurityGroupID).Scan(&groupTenant); e != nil || groupTenant != tid {
			return apiError{404, "Security group not found"}
		}
	}
	var previous string
	_ = a.DB.QueryRow("SELECT security_group_id FROM subnet_security_groups WHERE subnet_id=$1", sid).Scan(&previous)
	if r.Method == "DELETE" {
		if _, e := a.DB.Exec("DELETE FROM subnet_security_groups WHERE subnet_id=$1", sid); e != nil {
			return e
		}
	} else {
		if _, e := a.DB.Exec("INSERT INTO subnet_security_groups(subnet_id,security_group_id) VALUES($1,$2) ON CONFLICT(subnet_id) DO UPDATE SET security_group_id=EXCLUDED.security_group_id", sid, in.SecurityGroupID); e != nil {
			return e
		}
	}
	if e := a.syncNetworkPoliciesLocked(); e != nil {
		if previous != "" {
			_, _ = a.DB.Exec("INSERT INTO subnet_security_groups(subnet_id,security_group_id) VALUES($1,$2) ON CONFLICT(subnet_id) DO UPDATE SET security_group_id=EXCLUDED.security_group_id", sid, previous)
		} else {
			_, _ = a.DB.Exec("DELETE FROM subnet_security_groups WHERE subnet_id=$1", sid)
		}
		_ = a.syncNetworkPoliciesLocked()
		return apiError{503, "Network policy could not be applied; previous association restored: " + e.Error()}
	}
	jsonOut(w, 200, map[string]any{"enforced": true, "note": "Applied in the Docker engine's Linux network namespace. Physical LAN and bare-metal release qualification remain separate."})
	return nil
}

func (a *App) syncNetworkPolicies() error {
	return a.withPolicyLock(a.syncNetworkPoliciesLocked)
}

func (a *App) syncNetworkPoliciesLocked() error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	p := PolicyRequest{Resources: []PolicyResource{}, Links: []PolicyLink{}}
	rows, e := a.DB.QueryContext(ctx, "SELECT r.id,r.tenant_id,s.id,i.address,s.cidr,g.rules FROM subnet_security_groups b JOIN subnets s ON s.id=b.subnet_id JOIN network_security_groups g ON g.id=b.security_group_id JOIN ip_allocations i ON i.subnet_id=s.id JOIN resources r ON r.id=i.resource_id WHERE r.state NOT IN ('Deleted','Retained') ORDER BY r.id")
	if e != nil {
		return e
	}
	for rows.Next() {
		var item PolicyResource
		var rules []byte
		if e = rows.Scan(&item.ID, &item.TenantID, &item.SubnetID, &item.Address, &item.CIDR, &rules); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal(rules, &item.Rules); e != nil {
			rows.Close()
			return e
		}
		p.Resources = append(p.Resources, item)
	}
	rows.Close()
	// Avoid installing global hooks until an owner opts a subnet into enforcement.
	var oldState string
	_ = a.DB.QueryRowContext(ctx, "SELECT state FROM network_policy_status WHERE id=1").Scan(&oldState)
	if len(p.Resources) == 0 && oldState == "Not configured" {
		return nil
	}
	rows, e = a.DB.QueryContext(ctx, "SELECT b.bastion_id,b.target_id,r.type FROM bastion_targets b JOIN resources r ON r.id=b.target_id WHERE r.state='Running' ORDER BY b.bastion_id,b.target_id")
	if e != nil {
		return e
	}
	for rows.Next() {
		var source, target, kind string
		if rows.Scan(&source, &target, &kind) != nil {
			continue
		}
		port := 22
		if kind == "desktop-ubuntu" {
			port = 3389
		}
		p.Links = append(p.Links, PolicyLink{SourceID: source, TargetID: target, Port: port})
	}
	rows.Close()
	rows, e = a.DB.QueryContext(ctx, "SELECT id,source_id,target_id,port FROM service_connections ORDER BY id")
	if e != nil {
		return e
	}
	for rows.Next() {
		var link PolicyLink
		if rows.Scan(&link.ConnectorID, &link.SourceID, &link.TargetID, &link.Port) == nil {
			p.Links = append(p.Links, link)
		}
	}
	rows.Close()
	var response struct {
		Generation string `json:"generation"`
	}
	rows, e = a.DB.QueryContext(ctx, "SELECT e.resource_id FROM application_endpoints e JOIN resources r ON r.id=e.resource_id WHERE r.state='Running' ORDER BY e.resource_id")
	if e != nil {
		return e
	}
	for rows.Next() {
		var rid string
		if e = rows.Scan(&rid); e != nil {
			rows.Close()
			return e
		}
		p.Ingress = append(p.Ingress, rid)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	e = a.agent(ctx, "POST", "/network-policy", p, &response)
	if e != nil {
		_, _ = a.DB.Exec("UPDATE network_policy_status SET state='Degraded',error=$1,updated_at=now() WHERE id=1", e.Error())
		return e
	}
	_, e = a.DB.Exec("UPDATE network_policy_status SET state='Applied in Docker engine',generation=$1,error='',updated_at=now() WHERE id=1", response.Generation)
	return e
}

func renderPolicy(install string, endpoints []policyEndpoint, denied []string) (string, string, []string, error) {
	if len(install) != 32 || strings.Trim(install, "0123456789abcdef") != "" {
		return "", "", nil, errors.New("Invalid installation identity")
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].ID < endpoints[j].ID })
	sort.Strings(denied)
	for i := range endpoints {
		if e := validateNetworkRules(endpoints[i].Rules); e != nil {
			return "", "", nil, e
		}
		sort.Strings(endpoints[i].AllowsIn)
		sort.Strings(endpoints[i].AllowsOut)
		sort.Slice(endpoints[i].Rules, func(a, b int) bool { return endpoints[i].Rules[a].Priority < endpoints[i].Rules[b].Priority })
	}
	payload, _ := json.Marshal(struct {
		Endpoints []policyEndpoint
		Denied    []string
	}{endpoints, denied})
	hash := sha256.Sum256(payload)
	generation := hex.EncodeToString(hash[:])[:8]
	prefix := "KZ" + install[:8]
	gen := prefix + "G" + generation
	var declarations, rules []string
	names := map[string]bool{}
	addChain := func(name string) error {
		if names[name] {
			return errors.New("Policy chain identity collision")
		}
		names[name] = true
		declarations = append(declarations, ":"+name+" - [0:0]")
		return nil
	}
	for _, suffix := range []string{"F", "I", "O"} {
		_ = addChain(prefix + suffix)
		_ = addChain(gen + suffix)
		rules = append(rules, "-F "+prefix+suffix, "-A "+prefix+suffix+" -m comment --comment kzure:"+install+" -j "+gen+suffix)
	}
	addresses := []string{}
	for _, item := range endpoints {
		ip, e := netip.ParseAddr(item.Address)
		if e != nil || !ip.Is4() || !ip.IsPrivate() {
			return "", "", nil, errors.New("Invalid protected address")
		}
		if len(item.ID) != 32 || strings.Trim(item.ID, "0123456789abcdef") != "" {
			return "", "", nil, errors.New("Invalid protected resource")
		}
		addresses = append(addresses, item.Address)
		in, out := gen+"I"+item.ID[:6], gen+"O"+item.ID[:6]
		if e = addChain(in); e != nil {
			return "", "", nil, e
		}
		if e = addChain(out); e != nil {
			return "", "", nil, e
		}
		rules = append(rules, "-A "+gen+"F -s "+item.Address+" -j "+out, "-A "+gen+"F -d "+item.Address+" -j "+in, "-A "+gen+"I -s "+item.Address+" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN", "-A "+gen+"I -s "+item.Address+" -j DROP", "-A "+gen+"O -d "+item.Address+" -j "+in)
		for _, cidr := range denied {
			if _, e = netip.ParsePrefix(cidr); e != nil {
				return "", "", nil, e
			}
			rules = append(rules, "-A "+out+" -d "+cidr+" -j DROP")
		}
		for _, other := range endpoints {
			if other.TenantID != item.TenantID {
				rules = append(rules, "-A "+out+" -d "+other.Address+" -j DROP", "-A "+in+" -s "+other.Address+" -j DROP")
			}
		}
		// Ordered first-match user rules precede established state. Connection
		// tracking is also cleared for protected addresses when the generation changes.
		for _, rule := range item.Rules {
			chain := in
			if rule.Direction == "outbound" {
				chain = out
			}
			target := "RETURN"
			if rule.Action == "deny" {
				target = "DROP"
			}
			line, e := policyRuleLine(chain, rule, target)
			if e != nil {
				return "", "", nil, e
			}
			rules = append(rules, line)
		}
		for _, chain := range []string{in, out} {
			rules = append(rules, "-A "+chain+" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN")
		}
		for _, allow := range item.AllowsIn {
			rules = append(rules, "-A "+in+" "+allow+" -j RETURN")
		}
		for _, allow := range item.AllowsOut {
			rules = append(rules, "-A "+out+" "+allow+" -j RETURN")
		}
		rules = append(rules, "-A "+in+" -j DROP", "-A "+out+" -j DROP")
	}
	for _, suffix := range []string{"F", "I", "O"} {
		rules = append(rules, "-A "+gen+suffix+" -j RETURN")
	}
	// Flush only the new owned generation. This also makes replay idempotent.
	flush := []string{}
	for _, name := range declarations {
		chain := strings.Fields(strings.TrimPrefix(name, ":"))[0]
		if strings.HasPrefix(chain, gen) {
			flush = append(flush, "-F "+chain)
		}
	}
	text := "*filter\n" + strings.Join(declarations, "\n") + "\n" + strings.Join(flush, "\n") + "\n" + strings.Join(rules, "\n") + "\nCOMMIT\n"
	return text, generation, addresses, nil
}

func policyRuleLine(chain string, r NetworkRule, target string) (string, error) {
	line := "-A " + chain
	for i, cidr := range []string{r.Source, r.Destination} {
		if cidr != "*" {
			p, e := netip.ParsePrefix(cidr)
			if e != nil || !p.Addr().Is4() {
				return "", errors.New("Policy requires IPv4 CIDRs")
			}
			flag := " -s "
			if i == 1 {
				flag = " -d "
			}
			line += flag + p.String()
		}
	}
	if r.Protocol != "any" {
		if r.Protocol != "tcp" && r.Protocol != "udp" {
			return "", errors.New("Invalid rule protocol")
		}
		line += " -p " + r.Protocol
		if r.Port > 0 && r.Port <= 65535 {
			line += fmt.Sprintf(" --dport %d", r.Port)
		}
	} else if r.Port != 0 {
		return "", errors.New("Any-protocol rules require port 0")
	}
	return line + " -j " + target, nil
}

func validateNetworkRules(rules []NetworkRule) error {
	if len(rules) > 100 {
		return errors.New("At most 100 rules are supported")
	}
	priorities := map[int]bool{}
	for _, r := range rules {
		if r.Priority < 100 || r.Priority > 4096 || priorities[r.Priority] || r.Direction != "inbound" && r.Direction != "outbound" || r.Action != "allow" && r.Action != "deny" || r.Protocol != "tcp" && r.Protocol != "udp" && r.Protocol != "any" || r.Port < 0 || r.Port > 65535 || r.Protocol == "any" && r.Port != 0 {
			return errors.New("Invalid rule priority, direction, action, protocol or port")
		}
		priorities[r.Priority] = true
		for _, cidr := range []string{r.Source, r.Destination} {
			if cidr != "*" {
				prefix, e := netip.ParsePrefix(cidr)
				if e != nil || !prefix.Addr().Is4() {
					return errors.New("Rule addresses must be IPv4 CIDRs or *")
				}
			}
		}
	}
	return nil
}
