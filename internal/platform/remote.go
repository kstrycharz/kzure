package platform

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

func (a *App) remoteRoutes(secured, mux *http.ServeMux) {
	secured.HandleFunc("GET /api/ssh-keys", a.handle(a.listSSHKeys))
	secured.HandleFunc("POST /api/ssh-keys", a.handle(a.addSSHKey))
	secured.HandleFunc("DELETE /api/ssh-keys/{id}", a.handle(a.revokeSSHKey))
	secured.HandleFunc("GET /api/resources/{id}/remote-access", a.handle(a.remoteAccess))
	secured.HandleFunc("PUT /api/resources/{id}/ssh-keys/{key}", a.handle(a.bindSSHKey))
	secured.HandleFunc("DELETE /api/resources/{id}/ssh-keys/{key}", a.handle(a.bindSSHKey))
	secured.HandleFunc("PUT /api/resources/{id}/bastion-targets/{target}", a.handle(a.bindBastionTarget))
	secured.HandleFunc("DELETE /api/resources/{id}/bastion-targets/{target}", a.handle(a.bindBastionTarget))
	secured.HandleFunc("DELETE /api/remote-connections/{id}", a.handle(a.terminateRemote))
	// This endpoint accepts only a per-bastion credential, never a browser session
	// or a tenant-selected control URL. It cannot call arbitrary node operations.
	mux.HandleFunc("POST /api/internal/bastions/{id}/authorize", a.handle(a.authorizeBastion))
}

func (a *App) owner(r *http.Request, res Resource) error {
	if e := a.scope(r, res.TenantID, false); e != nil {
		return e
	}
	if actor(r).Admin {
		return nil
	}
	role, _ := a.role(actor(r).ID, res.TenantID)
	if role != "owner" {
		return apiError{403, "Tenant owner permission required"}
	}
	return nil
}

func (a *App) listSSHKeys(w http.ResponseWriter, r *http.Request) error {
	rows, e := a.DB.Query("SELECT id,name,public_key,fingerprint,created_at FROM ssh_keys WHERE user_id=$1 AND NOT revoked ORDER BY created_at DESC", actor(r).ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, key, fp string
		var at time.Time
		if e = rows.Scan(&id, &name, &key, &fp, &at); e != nil {
			return e
		}
		out = append(out, map[string]any{"id": id, "name": name, "publicKey": key, "fingerprint": fp, "createdAt": at})
	}
	jsonOut(w, 200, out)
	return rows.Err()
}

func (a *App) addSSHKey(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name      string `json:"name"`
		PublicKey string `json:"publicKey"`
	}
	if e := decode(r, &in); e != nil {
		return e
	}
	if len(strings.TrimSpace(in.Name)) < 2 || len(in.Name) > 80 {
		return bad("Enter a key name between 2 and 80 characters")
	}
	key, fp, e := normalizeSSHKey(in.PublicKey)
	if e != nil {
		return bad(e.Error())
	}
	var n int
	if e = a.DB.QueryRow("SELECT count(*) FROM ssh_keys WHERE user_id=$1 AND NOT revoked", actor(r).ID).Scan(&n); e != nil {
		return e
	}
	if n >= 20 {
		return apiError{409, "Maximum of 20 active keys per user"}
	}
	kid := id()
	e = a.DB.QueryRow("INSERT INTO ssh_keys(id,user_id,name,public_key,fingerprint) VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id,fingerprint) DO UPDATE SET revoked=false,name=EXCLUDED.name RETURNING id", kid, actor(r).ID, in.Name, key, fp).Scan(&kid)
	if e != nil {
		return e
	}
	jsonOut(w, 201, map[string]string{"id": kid, "fingerprint": fp})
	return nil
}

func (a *App) revokeSSHKey(w http.ResponseWriter, r *http.Request) error {
	result, e := a.DB.Exec("UPDATE ssh_keys SET revoked=true WHERE id=$1 AND user_id=$2", r.PathValue("id"), actor(r).ID)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return apiError{404, "Key not found"}
	}
	_, _ = a.DB.Exec("UPDATE remote_connections SET state='Closed',ended_at=now(),reason='SSH key revoked' WHERE key_id=$1 AND state='Active'", r.PathValue("id"))
	a.reconcileRemote()
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) authorizedKeys(rid string) []string {
	rows, e := a.DB.Query("SELECT k.public_key FROM workspace_ssh_keys b JOIN ssh_keys k ON k.id=b.key_id JOIN resources r ON r.id=b.resource_id JOIN users u ON u.id=k.user_id LEFT JOIN memberships m ON m.user_id=u.id AND m.tenant_id=r.tenant_id WHERE b.resource_id=$1 AND NOT k.revoked AND (u.admin OR m.role IN ('owner','contributor'))", rid)
	if e != nil {
		return nil
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if rows.Scan(&key) == nil {
			keys = append(keys, key)
		}
	}
	return keys
}

func (a *App) bindSSHKey(w http.ResponseWriter, r *http.Request) error {
	res, e := a.resource(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = a.owner(r, res); e != nil {
		return e
	}
	if !isSSHWorkspace(res.Type) {
		return bad("Select an SSH workspace")
	}
	var uid string
	var admin bool
	var role string
	e = a.DB.QueryRow("SELECT k.user_id,u.admin,COALESCE(m.role,'') FROM ssh_keys k JOIN users u ON u.id=k.user_id LEFT JOIN memberships m ON m.user_id=k.user_id AND m.tenant_id=$2 WHERE k.id=$1 AND NOT k.revoked", r.PathValue("key"), res.TenantID).Scan(&uid, &admin, &role)
	if e != nil || !admin && role != "owner" && role != "contributor" {
		return bad("Select an active key owned by an eligible tenant member")
	}
	action := "ssh.key.attach"
	if r.Method == "DELETE" {
		action = "ssh.key.detach"
		_, e = a.DB.Exec("DELETE FROM workspace_ssh_keys WHERE resource_id=$1 AND key_id=$2", res.ID, r.PathValue("key"))
		_, _ = a.DB.Exec("UPDATE remote_connections SET state='Closed',ended_at=now(),reason='Target access revoked' WHERE target_id=$1 AND key_id=$2 AND state='Active'", res.ID, r.PathValue("key"))
	} else {
		_, e = a.DB.Exec("INSERT INTO workspace_ssh_keys(resource_id,key_id,granted_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", res.ID, r.PathValue("key"), actor(r).ID)
	}
	if e != nil {
		return e
	}
	if res.State == "Running" {
		if e = a.agent(r.Context(), "POST", "/resources/"+res.ID+"/ssh-keys", map[string]any{"keys": a.authorizedKeys(res.ID)}, nil); e != nil {
			return e
		}
	}
	_, _ = a.DB.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,$4,$5)", id(), res.TenantID, actor(r).ID, action, res.ID)
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) targetIDs(bid string) []string {
	rows, e := a.DB.Query("SELECT target_id FROM bastion_targets WHERE bastion_id=$1", bid)
	if e != nil {
		return nil
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var rid string
		if rows.Scan(&rid) == nil {
			out = append(out, rid)
		}
	}
	return out
}

func (a *App) bindBastionTarget(w http.ResponseWriter, r *http.Request) error {
	bastion, e := a.resource(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = a.owner(r, bastion); e != nil {
		return e
	}
	if bastion.Type != "bastion" {
		return bad("Select a bastion")
	}
	target, e := a.resource(r.PathValue("target"))
	if e != nil || target.TenantID != bastion.TenantID {
		return apiError{404, "Target not found"}
	}
	if !isSSHWorkspace(target.Type) && target.Type != "desktop-ubuntu" {
		return bad("Select an SSH workspace or desktop target")
	}
	if r.Method == "DELETE" {
		_, e = a.DB.Exec("DELETE FROM bastion_targets WHERE bastion_id=$1 AND target_id=$2", bastion.ID, target.ID)
		_, _ = a.DB.Exec("UPDATE remote_connections SET state='Closed',ended_at=now(),reason='Bastion target removed' WHERE bastion_id=$1 AND target_id=$2 AND state='Active'", bastion.ID, target.ID)
	} else {
		if bastion.State != "Running" || target.State != "Running" {
			return apiError{409, "Start both resources before attaching a target"}
		}
		_, e = a.DB.Exec("INSERT INTO bastion_targets(bastion_id,target_id) VALUES($1,$2) ON CONFLICT DO NOTHING", bastion.ID, target.ID)
	}
	if e != nil {
		return e
	}
	if bastion.State == "Running" {
		e = a.agent(r.Context(), "POST", "/resources/"+bastion.ID+"/bastion-targets", map[string]any{"targets": a.targetIDs(bastion.ID)}, nil)
		if e != nil {
			return e
		}
	}
	if e=a.syncNetworkPolicies();e!=nil{return e}
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) remoteAccess(w http.ResponseWriter, r *http.Request) error {
	res, e := a.resource(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = a.scope(r, res.TenantID, false); e != nil {
		return e
	}
	role, _ := a.role(actor(r).ID, res.TenantID)
	manage := actor(r).Admin || role == "owner"
	out := map[string]any{"canManage": manage, "sshWorkspace": isSSHWorkspace(res.Type), "bastion": res.Type == "bastion", "desktop": res.Type == "desktop-ubuntu", "username": "kzure", "hostname": "r-" + res.ID + ".kzure", "endpoint": res.Endpoint, "browserDesktop": res.Type=="desktop-ubuntu"}
	var cipher string
	if e = a.DB.QueryRow("SELECT secret_cipher FROM resources WHERE id=$1", res.ID).Scan(&cipher); e != nil {
		return e
	}
	sec, e := a.unseal(cipher)
	if e != nil {
		return e
	}
	if sec["hostKey"] != "" {
		pub, fp, e := hostPublicKey(sec["hostKey"])
		if e != nil {
			return e
		}
		out["hostPublicKey"] = pub
		out["hostFingerprint"] = fp
	}
	rows, e := a.DB.Query("SELECT k.id,k.name,k.fingerprint,u.name,b.key_id IS NOT NULL FROM ssh_keys k JOIN users u ON u.id=k.user_id LEFT JOIN memberships m ON m.user_id=u.id AND m.tenant_id=$2 LEFT JOIN workspace_ssh_keys b ON b.key_id=k.id AND b.resource_id=$1 WHERE NOT k.revoked AND (u.admin OR m.role IN ('owner','contributor')) AND ($3 OR k.user_id=$4) ORDER BY u.name,k.name", res.ID, res.TenantID, manage, actor(r).ID)
	if e != nil {
		return e
	}
	keys := []map[string]any{}
	for rows.Next() {
		var kid, name, fp, user string
		var attached bool
		if e = rows.Scan(&kid, &name, &fp, &user, &attached); e != nil {
			rows.Close()
			return e
		}
		keys = append(keys, map[string]any{"id": kid, "name": name, "fingerprint": fp, "owner": user, "attached": attached})
	}
	rows.Close()
	out["keys"] = keys
	resources, e := a.resources(res.TenantID)
	if e != nil {
		return e
	}
	targets := []map[string]any{}
	bastions := []map[string]any{}
	bindings := a.targetIDs(res.ID)
	for _, candidate := range resources {
		if candidate.Type == "bastion" {
			var attached bool
			_ = a.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM bastion_targets WHERE bastion_id=$1 AND target_id=$2)", candidate.ID, res.ID).Scan(&attached)
			bastions = append(bastions, map[string]any{"id": candidate.ID, "name": candidate.Name, "endpoint": candidate.Endpoint, "attached": attached})
		}
		if isSSHWorkspace(candidate.Type) || candidate.Type == "desktop-ubuntu" {
			attached := false
			for _, rid := range bindings {
				if rid == candidate.ID {
					attached = true
				}
			}
			targets = append(targets, map[string]any{"id": candidate.ID, "name": candidate.Name, "type": candidate.Type, "state": candidate.State, "attached": attached})
		}
	}
	out["targets"] = targets
	out["bastions"] = bastions
	connections := []map[string]any{}
	rows, e = a.DB.Query("SELECT c.id,u.name,t.name,c.state,c.created_at,c.reason FROM remote_connections c JOIN users u ON u.id=c.user_id JOIN resources t ON t.id=c.target_id WHERE (c.bastion_id=$1 OR c.target_id=$1) AND ($2 OR c.user_id=$3) ORDER BY c.created_at DESC LIMIT 30", res.ID, manage, actor(r).ID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var sid, user, target, state, reason string
		var at time.Time
		if e = rows.Scan(&sid, &user, &target, &state, &at, &reason); e != nil {
			rows.Close()
			return e
		}
		connections = append(connections, map[string]any{"id": sid, "user": user, "target": target, "state": state, "createdAt": at, "reason": reason})
	}
	rows.Close()
	out["sessions"] = connections
	out["login"] = actor(r).Email
	jsonOut(w, 200, out)
	return nil
}

type BastionRequest struct {
	Action      string `json:"action"`
	Email       string `json:"email"`
	Fingerprint string `json:"fingerprint"`
	Target      string `json:"target"`
	Port        int    `json:"port"`
	SessionID   string `json:"sessionId"`
}
type BastionResponse struct {
	Allowed bool   `json:"allowed"`
	Address string `json:"address,omitempty"`
	UserID  string `json:"userId,omitempty"`
}

func (a *App) authorizeBastion(w http.ResponseWriter, r *http.Request) error {
	bid := r.PathValue("id")
	res, e := a.resource(bid)
	if e != nil || res.Type != "bastion" {
		return apiError{403, "Bastion not authorized"}
	}
	var cipher string
	if e = a.DB.QueryRow("SELECT secret_cipher FROM resources WHERE id=$1", bid).Scan(&cipher); e != nil {
		return e
	}
	sec, e := a.unseal(cipher)
	if e != nil {
		return e
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+sec["controlToken"])) != 1 {
		return apiError{403, "Bastion not authorized"}
	}
	var in BastionRequest
	if e = decode(r, &in); e != nil {
		return e
	}
	if len(in.SessionID) > 32 || len(in.Email) > 254 || len(in.Fingerprint) > 100 {
		return bad("Invalid access request")
	}
	if in.Action == "close" {
		_, e = a.DB.Exec("UPDATE remote_connections SET state='Closed',ended_at=COALESCE(ended_at,now()),reason=CASE WHEN reason='' THEN 'Connection closed' ELSE reason END WHERE id=$1 AND bastion_id=$2", in.SessionID, bid)
		if e != nil {
			return e
		}
		jsonOut(w, 200, BastionResponse{Allowed: true})
		return nil
	}
	if res.State != "Running" || res.Desired != "Running" {
		return apiError{403, "Bastion is not running"}
	}
	var uid, kid, role string
	var admin bool
	e = a.DB.QueryRow("SELECT u.id,k.id,u.admin,COALESCE(m.role,'') FROM users u JOIN ssh_keys k ON k.user_id=u.id LEFT JOIN memberships m ON m.user_id=u.id AND m.tenant_id=$3 WHERE lower(u.email)=lower($1) AND k.fingerprint=$2 AND NOT k.revoked", in.Email, in.Fingerprint, res.TenantID).Scan(&uid, &kid, &admin, &role)
	if e != nil || !admin && role != "owner" && role != "contributor" {
		return apiError{403, "Key or tenant permission denied"}
	}
	if in.Action == "auth" {
		jsonOut(w, 200, BastionResponse{Allowed: true, UserID: uid})
		return nil
	}
	if in.Action != "connect" && in.Action != "check" {
		return bad("Unsupported access action")
	}
	targetID := strings.TrimSuffix(strings.TrimPrefix(in.Target, "r-"), ".kzure")
	if len(targetID) != 32 || in.Target != "r-"+targetID+".kzure" {
		return apiError{403, "Only selected Kzure targets are allowed"}
	}
	target, e := a.resource(targetID)
	if e != nil || target.TenantID != res.TenantID || target.State != "Running" || target.Desired != "Running" {
		return apiError{403, "Target is unavailable"}
	}
	var attached bool
	if e = a.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM bastion_targets WHERE bastion_id=$1 AND target_id=$2)", bid, targetID).Scan(&attached); e != nil || !attached {
		return apiError{403, "Target is not attached to this bastion"}
	}
	if isSSHWorkspace(target.Type) && in.Port == 22 {
		var keyBound bool
		if e = a.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM workspace_ssh_keys WHERE resource_id=$1 AND key_id=$2)", targetID, kid).Scan(&keyBound); e != nil || !keyBound {
			return apiError{403, "Key is not authorized for this target"}
		}
	} else if target.Type == "desktop-ubuntu" && in.Port == 3389 {
		if !admin && role != "owner" {
			return apiError{403, "Desktop currently requires owner permission"}
		}
	} else {
		return apiError{403, "Protocol or port not permitted"}
	}
	if in.Action == "check" {
		result, e := a.DB.Exec("UPDATE remote_connections SET last_seen=now() WHERE id=$1 AND bastion_id=$2 AND target_id=$3 AND key_id=$4 AND user_id=$5 AND state='Active' AND created_at>now()-interval '2 hours'", in.SessionID, bid, targetID, kid, uid)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return apiError{403, "Connection expired or terminated"}
		}
		jsonOut(w, 200, BastionResponse{Allowed: true})
		return nil
	}
	if len(in.SessionID) != 32 || strings.Trim(in.SessionID, "0123456789abcdef") != "" {
		return bad("Invalid connection identity")
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("SELECT pg_advisory_xact_lock(7392104)"); e != nil {
		return e
	}
	_, _ = tx.Exec("UPDATE remote_connections SET state='Closed',ended_at=now(),reason='Gateway lease expired' WHERE state='Active' AND last_seen<now()-interval '20 seconds'")
	var total, userN, targetN int
	if e = tx.QueryRow("SELECT count(*),count(*) FILTER(WHERE user_id=$1),count(*) FILTER(WHERE target_id=$2) FROM remote_connections WHERE state='Active'", uid, targetID).Scan(&total, &userN, &targetN); e != nil {
		return e
	}
	if total >= 32 || userN >= 5 || targetN >= 4 {
		return apiError{429, "Remote connection limit reached"}
	}
	if _, e = tx.Exec("INSERT INTO remote_connections(id,bastion_id,target_id,user_id,key_id) VALUES($1,$2,$3,$4,$5)", in.SessionID, bid, targetID, uid, kid); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,'remote.connect',$4)", id(), res.TenantID, uid, targetID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	var endpoint struct {
		Address string `json:"address"`
	}
	if e = a.agent(r.Context(), "GET", "/resources/"+targetID+"/network", nil, &endpoint); e != nil {
		return e
	}
	jsonOut(w, 200, BastionResponse{Allowed: true, Address: net.JoinHostPort(endpoint.Address, fmt.Sprint(in.Port)), UserID: uid})
	return nil
}

func (a *App) terminateRemote(w http.ResponseWriter, r *http.Request) error {
	var bid, uid string
	if e := a.DB.QueryRow("SELECT bastion_id,user_id FROM remote_connections WHERE id=$1", r.PathValue("id")).Scan(&bid, &uid); e != nil {
		return e
	}
	res, e := a.resource(bid)
	if e != nil {
		return e
	}
	if e = a.scope(r, res.TenantID, false); e != nil {
		return e
	}
	if uid != actor(r).ID {
		if e = a.owner(r, res); e != nil {
			return e
		}
	}
	_, e = a.DB.Exec("UPDATE remote_connections SET state='Closed',ended_at=now(),reason='Disconnected by user' WHERE id=$1", r.PathValue("id"))
	if e != nil {
		return e
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) reconcileRemote() {
	rows, e := a.DB.Query("SELECT id,type FROM resources WHERE state='Running' AND (type LIKE 'workspace-ssh-%' OR type='bastion')")
	if e != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var rid, kind string
		if rows.Scan(&rid, &kind) != nil {
			continue
		}
		if kind == "bastion" {
			_ = a.agent(context.Background(), "POST", "/resources/"+rid+"/bastion-targets", map[string]any{"targets": a.targetIDs(rid)}, nil)
		} else {
			_ = a.agent(context.Background(), "POST", "/resources/"+rid+"/ssh-keys", map[string]any{"keys": a.authorizedKeys(rid)}, nil)
		}
	}
}

var _ = json.Valid
