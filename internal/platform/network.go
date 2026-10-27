package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

const networkSchema = `
CREATE TABLE IF NOT EXISTS virtual_networks(id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES tenants(id),subscription_id text NOT NULL REFERENCES subscriptions(id),name text NOT NULL,cidr text NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(subscription_id,name));
CREATE TABLE IF NOT EXISTS subnets(id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES tenants(id),vnet_id text NOT NULL REFERENCES virtual_networks(id),name text NOT NULL,cidr text NOT NULL,state text NOT NULL DEFAULT 'Pending',error text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(vnet_id,name));
CREATE TABLE IF NOT EXISTS ip_allocations(subnet_id text NOT NULL REFERENCES subnets(id),resource_id text NOT NULL REFERENCES resources(id),address text NOT NULL,PRIMARY KEY(subnet_id,address),UNIQUE(resource_id));
CREATE TABLE IF NOT EXISTS network_security_groups(id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES tenants(id),name text NOT NULL,rules jsonb NOT NULL DEFAULT '[]',revision int NOT NULL DEFAULT 1,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS service_connections(id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES tenants(id),source_id text NOT NULL REFERENCES resources(id),target_id text NOT NULL REFERENCES resources(id),port int NOT NULL,state text NOT NULL DEFAULT 'Pending',error text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(source_id,target_id,port));
`

type NetworkPlan struct {
	ID      string `json:"id"`
	CIDR    string `json:"cidr"`
	Address string `json:"address"`
}
type NetworkRule struct {
	Name        string `json:"name"`
	Priority    int    `json:"priority"`
	Direction   string `json:"direction"`
	Action      string `json:"action"`
	Protocol    string `json:"protocol"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Port        int    `json:"port"`
}

func privatePrefix(raw string) (netip.Prefix, error) {
	p, e := netip.ParsePrefix(raw)
	if e != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() || p != p.Masked() || p.Bits() < 16 || p.Bits() > 28 {
		return netip.Prefix{}, errors.New("Use an aligned private IPv4 range between /16 and /28")
	}
	return p, nil
}

func (a *App) networkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/networking", a.handle(a.networkOverview))
	mux.HandleFunc("POST /api/virtual-networks", a.handle(a.createVNet))
	mux.HandleFunc("POST /api/subnets", a.handle(a.createSubnet))
	mux.HandleFunc("DELETE /api/subnets/{id}", a.handle(a.deleteSubnet))
	mux.HandleFunc("DELETE /api/virtual-networks/{id}", a.handle(a.deleteVNet))
	mux.HandleFunc("POST /api/network-security-groups", a.handle(a.saveNSG))
	mux.HandleFunc("PUT /api/network-security-groups/{id}", a.handle(a.saveNSG))
	mux.HandleFunc("POST /api/service-connections", a.handle(a.createServiceConnection))
	mux.HandleFunc("DELETE /api/service-connections/{id}", a.handle(a.deleteServiceConnection))
}

func (a *App) networkOverview(w http.ResponseWriter, r *http.Request) error {
	tid := r.URL.Query().Get("tenant")
	if e := a.scope(r, tid, false); e != nil {
		return e
	}
	out := map[string]any{"mode": "Limited networking", "nsgEnforced": false, "note": "Private Docker subnets and explicit TCP connectors work. Associate a security group with a subnet to apply IPv4 default-deny rules. No subnet policy is currently applied. Physical LAN, dedicated-host and IPv6 qualification remain open."}
	var policyState, generation, policyError string
	var protected int
	if e := a.DB.QueryRow("SELECT state,generation,error FROM network_policy_status WHERE id=1").Scan(&policyState, &generation, &policyError); e != nil {
		return e
	}
	if e := a.DB.QueryRow("SELECT count(*) FROM subnet_security_groups b JOIN subnets s ON s.id=b.subnet_id WHERE s.tenant_id=$1", tid).Scan(&protected); e != nil {
		return e
	}
	out["policyState"], out["policyGeneration"], out["policyError"] = policyState, generation, policyError
	if protected > 0 {
		out["mode"] = "Subnet security policy"
		out["nsgEnforced"] = policyState == "Applied in Docker engine"
		out["note"] = "Only subnets with an attached security group are protected. Rules apply in the Docker engine's IPv4 network namespace; physical LAN and dedicated-host qualification remain separate."
	}
	for key, q := range map[string]string{
		"virtualNetworks": "SELECT jsonb_build_object('id',id,'name',name,'cidr',cidr,'subscriptionId',subscription_id) FROM virtual_networks WHERE tenant_id=$1 ORDER BY name",
		"subnets":         "SELECT jsonb_build_object('id',s.id,'name',s.name,'cidr',s.cidr,'vnetId',s.vnet_id,'vnetName',v.name,'state',s.state,'error',s.error,'securityGroupId',COALESCE((SELECT security_group_id FROM subnet_security_groups b WHERE b.subnet_id=s.id),''),'allocated',(SELECT count(*) FROM ip_allocations i WHERE i.subnet_id=s.id)) FROM subnets s JOIN virtual_networks v ON v.id=s.vnet_id WHERE s.tenant_id=$1 ORDER BY s.name",
		"securityGroups":  "SELECT jsonb_build_object('id',g.id,'name',g.name,'rules',g.rules,'revision',g.revision,'enforced',EXISTS(SELECT 1 FROM subnet_security_groups b WHERE b.security_group_id=g.id) AND (SELECT state='Applied in Docker engine' FROM network_policy_status WHERE id=1)) FROM network_security_groups g WHERE g.tenant_id=$1 ORDER BY g.name",
		"connections":     "SELECT jsonb_build_object('id',c.id,'sourceId',c.source_id,'sourceName',s.name,'targetId',c.target_id,'targetName',t.name,'port',c.port,'state',c.state,'error',c.error,'hostname','service-'||c.id||'.kzure') FROM service_connections c JOIN resources s ON s.id=c.source_id JOIN resources t ON t.id=c.target_id WHERE c.tenant_id=$1 ORDER BY c.created_at",
		"interfaces":      "SELECT jsonb_build_object('resourceId',r.id,'name',r.name,'address',i.address,'subnetId',i.subnet_id,'dns','r-'||r.id||'.kzure','state',r.state) FROM ip_allocations i JOIN resources r ON r.id=i.resource_id WHERE r.tenant_id=$1 ORDER BY r.name",
	} {
		rows, e := a.DB.Query(q, tid)
		if e != nil {
			return e
		}
		items := []json.RawMessage{}
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return e
			}
			items = append(items, json.RawMessage(raw))
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		out[key] = items
	}
	jsonOut(w, 200, out)
	return nil
}

func (a *App) createVNet(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		TenantID       string `json:"tenantId"`
		SubscriptionID string `json:"subscriptionId"`
		Name           string `json:"name"`
		CIDR           string `json:"cidr"`
	}
	if e := decode(r, &in); e != nil {
		return e
	}
	if e := a.scope(r, in.TenantID, true); e != nil {
		return e
	}
	if !validName.MatchString(in.Name) {
		return bad("Use a resource-style network name")
	}
	p, e := privatePrefix(in.CIDR)
	if e != nil {
		return bad(e.Error())
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("SELECT pg_advisory_xact_lock(7392103)"); e != nil {
		return e
	}
	var tid string
	if e = tx.QueryRow("SELECT tenant_id FROM subscriptions WHERE id=$1", in.SubscriptionID).Scan(&tid); e != nil || tid != in.TenantID {
		return bad("Subscription does not belong to this tenant")
	}
	rows, e := tx.Query("SELECT cidr FROM virtual_networks")
	if e != nil {
		return e
	}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		other, _ := netip.ParsePrefix(raw)
		if p.Overlaps(other) {
			rows.Close()
			return apiError{409, "Address range overlaps an existing virtual network"}
		}
	}
	rows.Close()
	var existing []string
	if e = a.agent(r.Context(), "GET", "/network-ranges", nil, &existing); e != nil {
		return apiError{503, "Cannot inspect Docker address ranges"}
	}
	for _, raw := range existing {
		other, e := netip.ParsePrefix(raw)
		if e == nil && p.Overlaps(other) {
			return apiError{409, "Address range overlaps an existing Docker network"}
		}
	}
	nid := id()
	if _, e = tx.Exec("INSERT INTO virtual_networks(id,tenant_id,subscription_id,name,cidr) VALUES($1,$2,$3,$4,$5)", nid, in.TenantID, in.SubscriptionID, in.Name, in.CIDR); e != nil {
		return apiError{409, "Network name already exists"}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	jsonOut(w, 201, map[string]string{"id": nid})
	return nil
}

func (a *App) createSubnet(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		TenantID string `json:"tenantId"`
		VNetID   string `json:"vnetId"`
		Name     string `json:"name"`
		CIDR     string `json:"cidr"`
	}
	if e := decode(r, &in); e != nil {
		return e
	}
	if e := a.scope(r, in.TenantID, true); e != nil {
		return e
	}
	if !validName.MatchString(in.Name) {
		return bad("Use a resource-style subnet name")
	}
	p, e := privatePrefix(in.CIDR)
	if e != nil || p.Bits() < 24 {
		return bad("Subnets require an aligned private IPv4 range between /24 and /28")
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("SELECT pg_advisory_xact_lock(7392103)"); e != nil {
		return e
	}
	var cidr string
	if e = tx.QueryRow("SELECT cidr FROM virtual_networks WHERE id=$1 AND tenant_id=$2", in.VNetID, in.TenantID).Scan(&cidr); e != nil {
		return apiError{404, "Virtual network not found"}
	}
	parent, _ := netip.ParsePrefix(cidr)
	if !parent.Contains(p.Addr()) || p.Bits() < parent.Bits() {
		return bad("Subnet must fit inside the virtual network")
	}
	rows, e := tx.Query("SELECT cidr FROM subnets WHERE vnet_id=$1", in.VNetID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		other, _ := netip.ParsePrefix(raw)
		if p.Overlaps(other) {
			rows.Close()
			return apiError{409, "Subnet overlaps an existing subnet"}
		}
	}
	rows.Close()
	sid := id()
	if _, e = tx.Exec("INSERT INTO subnets(id,tenant_id,vnet_id,name,cidr) VALUES($1,$2,$3,$4,$5)", sid, in.TenantID, in.VNetID, in.Name, in.CIDR); e != nil {
		return apiError{409, "Subnet name already exists"}
	}
	if e = a.agent(r.Context(), "POST", "/networks", map[string]string{"id": sid, "tenantId": in.TenantID, "cidr": in.CIDR}, nil); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE subnets SET state='Ready' WHERE id=$1", sid); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	jsonOut(w, 201, map[string]string{"id": sid})
	return nil
}

func (a *App) deleteSubnet(w http.ResponseWriter, r *http.Request) error {
	var tid string
	if e := a.DB.QueryRow("SELECT tenant_id FROM subnets WHERE id=$1", r.PathValue("id")).Scan(&tid); e != nil {
		return e
	}
	if e := a.scope(r, tid, true); e != nil {
		return e
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("SELECT pg_advisory_xact_lock(7392101),pg_advisory_xact_lock(7392103)"); e != nil {
		return e
	}
	var n int
	if e = tx.QueryRow("SELECT count(*) FROM ip_allocations WHERE subnet_id=$1", r.PathValue("id")).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return apiError{409, "Remove attached resources before deleting this subnet"}
	}
	if e=tx.QueryRow("SELECT count(*) FROM subnet_security_groups WHERE subnet_id=$1",r.PathValue("id")).Scan(&n);e!=nil{return e};if n>0{return apiError{409,"Remove the subnet security group before deleting this subnet"}}
	if e = a.agent(r.Context(), "DELETE", "/networks/"+r.PathValue("id"), nil, nil); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM subnets WHERE id=$1", r.PathValue("id")); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) deleteVNet(w http.ResponseWriter, r *http.Request) error {
	var tid string
	if e := a.DB.QueryRow("SELECT tenant_id FROM virtual_networks WHERE id=$1", r.PathValue("id")).Scan(&tid); e != nil {
		return e
	}
	if e := a.scope(r, tid, true); e != nil {
		return e
	}
	var n int
	if e := a.DB.QueryRow("SELECT count(*) FROM subnets WHERE vnet_id=$1", r.PathValue("id")).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return apiError{409, "Delete this network's subnets first"}
	}
	if _, e := a.DB.Exec("DELETE FROM virtual_networks WHERE id=$1", r.PathValue("id")); e != nil {
		return e
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) reserveNetwork(tx *sql.Tx, resID, tid string, spec Spec) (Spec, error) {
	if spec.SubnetID == "" {
		return spec, nil
	}
	var cidr string
	if e := tx.QueryRow("SELECT cidr FROM subnets WHERE id=$1 AND tenant_id=$2 AND state='Ready' FOR UPDATE", spec.SubnetID, tid).Scan(&cidr); e != nil {
		return spec, bad("Select a ready subnet in this tenant")
	}
	p, e := netip.ParsePrefix(cidr)
	if e != nil {
		return spec, e
	}
	used := map[string]bool{}
	rows, e := tx.Query("SELECT address FROM ip_allocations WHERE subnet_id=$1", spec.SubnetID)
	if e != nil {
		return spec, e
	}
	for rows.Next() {
		var ip string
		_ = rows.Scan(&ip)
		used[ip] = true
	}
	rows.Close()
	var occupied []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = a.agent(ctx, "GET", "/networks/"+spec.SubnetID+"/addresses", nil, &occupied); e != nil {
		return spec, apiError{503, "Cannot reserve an address while the subnet is unavailable"}
	}
	for _, ip := range occupied {
		used[ip] = true
	}
	// Workspaces use the lower half; managed gateways/connectors use the upper
	// half. This avoids races with infrastructure network attachments.
	staticRange := netip.PrefixFrom(p.Addr(), p.Bits()+1)
	addr := p.Addr().Next().Next()
	for staticRange.Contains(addr) && staticRange.Contains(addr.Next()) {
		if !used[addr.String()] {
			_, e = tx.Exec("INSERT INTO ip_allocations(subnet_id,resource_id,address) VALUES($1,$2,$3)", spec.SubnetID, resID, addr.String())
			return spec, e
		}
		addr = addr.Next()
	}
	return spec, apiError{409, "Subnet has no free private addresses"}
}

func (a *App) networkFor(r Resource) (*NetworkPlan, error) {
	if r.Spec.SubnetID == "" {
		return nil, nil
	}
	var n NetworkPlan
	n.ID = r.Spec.SubnetID
	e := a.DB.QueryRow("SELECT s.cidr,i.address FROM subnets s JOIN ip_allocations i ON i.subnet_id=s.id WHERE s.id=$1 AND s.tenant_id=$2 AND i.resource_id=$3", n.ID, r.TenantID, r.ID).Scan(&n.CIDR, &n.Address)
	return &n, e
}

func (a *App) reconcileNetworks() error {
	rows, e := a.DB.Query("SELECT id,tenant_id,cidr FROM subnets WHERE state='Ready' ORDER BY id")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var sid, tid, cidr string
		if e = rows.Scan(&sid, &tid, &cidr); e != nil {
			return e
		}
		if e = a.agent(context.Background(), "POST", "/networks", map[string]string{"id": sid, "tenantId": tid, "cidr": cidr}, nil); e != nil {
			return e
		}
	}
	return rows.Err()
}

func (a *App) saveNSG(w http.ResponseWriter, r *http.Request) error {
	return a.withPolicyLock(func() error { return a.saveNSGLocked(w, r) })
}

func (a *App) saveNSGLocked(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		TenantID string        `json:"tenantId"`
		Name     string        `json:"name"`
		Rules    []NetworkRule `json:"rules"`
	}
	if e := decode(r, &in); e != nil {
		return e
	}
	if e := a.scope(r, in.TenantID, true); e != nil {
		return e
	}
	if e := validateNetworkRules(in.Rules); e != nil {
		return bad(e.Error())
	}
	if !validName.MatchString(in.Name) || len(in.Rules) > 100 {
		return bad("Use a valid name and at most 100 rules")
	}
	priorities := map[int]bool{}
	for _, rule := range in.Rules {
		if rule.Priority < 100 || rule.Priority > 4096 || priorities[rule.Priority] || rule.Direction != "inbound" && rule.Direction != "outbound" || rule.Action != "allow" && rule.Action != "deny" || rule.Protocol != "tcp" && rule.Protocol != "udp" && rule.Protocol != "any" || rule.Port < 0 || rule.Port > 65535 {
			return bad("Invalid or duplicate rule priority, direction, action, protocol or port")
		}
		priorities[rule.Priority] = true
		for _, cidr := range []string{rule.Source, rule.Destination} {
			if cidr != "*" {
				if _, e := netip.ParsePrefix(cidr); e != nil {
					return bad("Rule addresses must be CIDRs or *")
				}
			}
		}
	}
	raw, _ := json.Marshal(in.Rules)
	sid := r.PathValue("id")
	if sid == "" {
		sid = id()
		if _, e := a.DB.Exec("INSERT INTO network_security_groups(id,tenant_id,name,rules) VALUES($1,$2,$3,$4)", sid, in.TenantID, in.Name, raw); e != nil {
			return e
		}
	} else {
		var previousName string
		var previousRules []byte
		var previousRevision int
		if e := a.DB.QueryRow("SELECT name,rules,revision FROM network_security_groups WHERE id=$1 AND tenant_id=$2", sid, in.TenantID).Scan(&previousName, &previousRules, &previousRevision); e != nil {
			return apiError{404, "Security group not found"}
		}
		var attached int
		if e := a.DB.QueryRow("SELECT count(*) FROM subnet_security_groups WHERE security_group_id=$1", sid).Scan(&attached); e != nil {
			return e
		}
		if attached > 0 {
			if e := a.owner(r, Resource{TenantID: in.TenantID}); e != nil {
				return e
			}
		}
		result, e := a.DB.Exec("UPDATE network_security_groups SET name=$1,rules=$2,revision=revision+1 WHERE id=$3 AND tenant_id=$4", in.Name, raw, sid, in.TenantID)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return apiError{404, "Security group not found"}
		}
		if attached > 0 {
			if e = a.syncNetworkPoliciesLocked(); e != nil {
				_, _ = a.DB.Exec("UPDATE network_security_groups SET name=$1,rules=$2,revision=$3 WHERE id=$4", previousName, previousRules, previousRevision, sid)
				_ = a.syncNetworkPoliciesLocked()
				return apiError{503, "Policy update failed; previous revision restored: " + e.Error()}
			}
		}
	}
	var enforced bool
	_ = a.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM subnet_security_groups WHERE security_group_id=$1)", sid).Scan(&enforced)
	jsonOut(w, 200, map[string]any{"id": sid, "enforced": enforced})
	return nil
}

func (a *App) createServiceConnection(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		SourceID string `json:"sourceId"`
		TargetID string `json:"targetId"`
	}
	if e := decode(r, &in); e != nil {
		return e
	}
	source, e := a.resource(in.SourceID)
	if e != nil {
		return e
	}
	if e = a.scope(r, source.TenantID, true); e != nil {
		return e
	}
	target, e := a.resource(in.TargetID)
	if e != nil || target.TenantID != source.TenantID {
		return apiError{404, "Target not found"}
	}
	if source.ID == target.ID || source.State != "Running" || target.State != "Running" {
		return bad("Select two different running resources")
	}
	item, _ := Item(target.Type)
	port := item.Port
	if isSSHWorkspace(target.Type) {
		port = 22
	}
	if port == 0 || target.Type == "bastion" {
		return bad("Target has no supported service port")
	}
	cid := id()
	if _, e = a.DB.Exec("INSERT INTO service_connections(id,tenant_id,source_id,target_id,port) VALUES($1,$2,$3,$4,$5)", cid, source.TenantID, source.ID, target.ID, port); e != nil {
		return apiError{409, "Connection already exists"}
	}
	e = a.agent(r.Context(), "POST", "/connectors", map[string]any{"id": cid, "sourceId": source.ID, "targetId": target.ID, "tenantId": source.TenantID, "port": port}, nil)
	if e != nil {
		_, _ = a.DB.Exec("UPDATE service_connections SET state='Failed',error=$2 WHERE id=$1", cid, e.Error())
		return e
	}
	_, e = a.DB.Exec("UPDATE service_connections SET state='Ready' WHERE id=$1", cid)
	if e != nil {
		return e
	}
	if e=a.syncNetworkPolicies();e!=nil{return e}
	jsonOut(w, 201, map[string]string{"id": cid, "hostname": "service-" + cid + ".kzure"})
	return nil
}

func (a *App) deleteServiceConnection(w http.ResponseWriter, r *http.Request) error {
	var tid string
	if e := a.DB.QueryRow("SELECT tenant_id FROM service_connections WHERE id=$1", r.PathValue("id")).Scan(&tid); e != nil {
		return e
	}
	if e := a.scope(r, tid, true); e != nil {
		return e
	}
	if e := a.agent(r.Context(), "DELETE", "/connectors/"+r.PathValue("id"), nil, nil); e != nil {
		return e
	}
	_, e := a.DB.Exec("DELETE FROM service_connections WHERE id=$1", r.PathValue("id"))
	if e != nil {
		return e
	}
	if e=a.syncNetworkPolicies();e!=nil{return e}
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}

// A reconciliation pass repairs failed connectors without changing their IDs.
func (a *App) reconcileConnections() {
	rows, e := a.DB.Query("SELECT id,tenant_id,source_id,target_id,port FROM service_connections")
	if e != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, tid, source, target string
		var port int
		if rows.Scan(&id, &tid, &source, &target, &port) != nil {
			continue
		}
		e = a.agent(context.Background(), "POST", "/connectors", map[string]any{"id": id, "tenantId": tid, "sourceId": source, "targetId": target, "port": port}, nil)
		if e == nil {
			_, _ = a.DB.Exec("UPDATE service_connections SET state='Ready',error='' WHERE id=$1", id)
		} else {
			_, _ = a.DB.Exec("UPDATE service_connections SET state='Degraded',error=$2 WHERE id=$1", id, fmt.Sprint(e))
		}
	}
}

var _ = strings.TrimSpace

