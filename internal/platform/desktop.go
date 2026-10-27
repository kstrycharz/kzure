package platform

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const desktopSchema = `
CREATE TABLE IF NOT EXISTS desktop_sessions(id text PRIMARY KEY,user_id text NOT NULL REFERENCES users(id),tenant_id text NOT NULL REFERENCES tenants(id),resource_id text NOT NULL REFERENCES resources(id),bastion_id text NOT NULL REFERENCES resources(id),container_id text NOT NULL,ticket_hash text NOT NULL,auth_hash text NOT NULL,state text NOT NULL DEFAULT 'Pending',expires_at timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),last_seen timestamptz NOT NULL DEFAULT now(),ended_at timestamptz,reason text NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS desktop_session_leases ON desktop_sessions(state,last_seen);
`

func (a *App) desktopRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/resources/{id}/desktop-sessions", a.handle(a.createDesktopSession))
	mux.HandleFunc("GET /api/desktop-sessions/{id}/stream", a.desktopStream)
	mux.HandleFunc("DELETE /api/desktop-sessions/{id}", a.handle(a.deleteDesktopSession))
}

func (a *App) desktopPermission(r *http.Request, target Resource, bid string) error {
	if e := a.owner(r, target); e != nil {
		return e
	}
	if target.Type != "desktop-ubuntu" {
		return bad("Select an Ubuntu desktop")
	}
	if target.State != "Running" || target.Desired != "Running" {
		return apiError{409, "Start the desktop before connecting"}
	}
	b, e := a.resource(bid)
	if e != nil || b.TenantID != target.TenantID || b.Type != "bastion" || b.State != "Running" || b.Desired != "Running" {
		return apiError{404, "Running bastion not found"}
	}
	var attached bool
	if e = a.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM bastion_targets WHERE bastion_id=$1 AND target_id=$2)", bid, target.ID).Scan(&attached); e != nil {
		return e
	}
	if !attached {
		return apiError{403, "Attach this desktop to the selected bastion first"}
	}
	return nil
}

func (a *App) createDesktopSession(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		BastionID string `json:"bastionId"`
	}
	if e := decode(r, &in); e != nil {
		return e
	}
	target, e := a.resource(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = a.desktopPermission(r, target, in.BastionID); e != nil {
		return e
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("SELECT pg_advisory_xact_lock(7392102)"); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE desktop_sessions SET state='Closed',ended_at=now(),reason='Session lease expired' WHERE (state='Pending' AND expires_at<now()) OR (state='Active' AND last_seen<now()-interval '30 seconds')"); e != nil {
		return e
	}
	var total, userN, targetN int
	if e = tx.QueryRow("SELECT count(*),count(*) FILTER(WHERE user_id=$1),count(*) FILTER(WHERE resource_id=$2) FROM desktop_sessions WHERE state IN ('Pending','Active')", actor(r).ID, target.ID).Scan(&total, &userN, &targetN); e != nil {
		return e
	}
	if total >= 16 || userN >= 3 || targetN >= 2 {
		return apiError{429, "Desktop session limit reached"}
	}
	sid, ticket := id(), token()
	cookie, _ := r.Cookie("kzure_session")
	if _, e = tx.Exec("INSERT INTO desktop_sessions(id,user_id,tenant_id,resource_id,bastion_id,container_id,ticket_hash,auth_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now()+interval '30 seconds')", sid, actor(r).ID, target.TenantID, target.ID, in.BastionID, target.ContainerID, hashToken(ticket), hashToken(cookie.Value)); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	jsonOut(w, 201, map[string]any{"id": sid, "ticket": ticket, "expiresIn": 30})
	return nil
}

func (a *App) deleteDesktopSession(w http.ResponseWriter, r *http.Request) error {
	var rid, uid string
	if e := a.DB.QueryRow("SELECT resource_id,user_id FROM desktop_sessions WHERE id=$1", r.PathValue("id")).Scan(&rid, &uid); e != nil {
		return e
	}
	target, e := a.resource(rid)
	if e != nil {
		return e
	}
	if e = a.scope(r, target.TenantID, false); e != nil {
		return e
	}
	if actor(r).ID != uid {
		if e = a.owner(r, target); e != nil {
			return e
		}
	}
	_, e = a.DB.Exec("UPDATE desktop_sessions SET state='Closed',ended_at=now(),reason='Disconnected by user' WHERE id=$1", r.PathValue("id"))
	if e != nil {
		return e
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) desktopStream(w http.ResponseWriter, r *http.Request) {
	if !terminalOrigin(r) {
		jsonOut(w, 403, map[string]string{"error": "Origin not allowed"})
		return
	}
	sid := r.PathValue("id")
	var rid, bid, uid, generation string
	e := a.DB.QueryRow("SELECT resource_id,bastion_id,user_id,container_id FROM desktop_sessions WHERE id=$1", sid).Scan(&rid, &bid, &uid, &generation)
	if e != nil || uid != actor(r).ID {
		jsonOut(w, 404, map[string]string{"error": "Session not found"})
		return
	}
	target, e := a.resource(rid)
	if e != nil {
		jsonOut(w, 404, map[string]string{"error": "Desktop not found"})
		return
	}
	if e = a.desktopPermission(r, target, bid); e != nil {
		a.handle(func(http.ResponseWriter, *http.Request) error { return e })(w, r)
		return
	}
	if target.ContainerID != generation {
		jsonOut(w, 409, map[string]string{"error": "Desktop changed; reconnect"})
		return
	}
	ws, e := (&websocket.Upgrader{CheckOrigin: terminalOrigin, Subprotocols: []string{"guacamole"}}).Upgrade(w, r, nil)
	if e != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(65536)
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth struct {
		Type   string `json:"type"`
		Ticket string `json:"ticket"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if e = ws.ReadJSON(&auth); e != nil || auth.Type != "auth" {
		return
	}
	cookie, _ := r.Cookie("kzure_session")
	var consumed string
	e = a.DB.QueryRow("UPDATE desktop_sessions SET state='Active',last_seen=now() WHERE id=$1 AND user_id=$2 AND state='Pending' AND ticket_hash=$3 AND auth_hash=$4 AND expires_at>now() RETURNING id", sid, uid, hashToken(auth.Ticket), hashToken(cookie.Value)).Scan(&consumed)
	if e != nil {
		_ = ws.WriteJSON(terminalFrame{Type: "error", Data: "Connection ticket expired or already used"})
		return
	}
	reason := "Connection closed"
	defer func() {
		_, _ = a.DB.Exec("UPDATE desktop_sessions SET state='Closed',ended_at=COALESCE(ended_at,now()),reason=CASE WHEN reason='' THEN $2 ELSE reason END WHERE id=$1", sid, reason)
		_, _ = a.DB.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,'desktop.disconnect',$4)", id(), target.TenantID, uid, rid)
	}()
	var cipher string
	if e = a.DB.QueryRow("SELECT secret_cipher FROM resources WHERE id=$1", rid).Scan(&cipher); e != nil {
		return
	}
	secrets, e := a.unseal(cipher)
	if e != nil {
		return
	}
	agentURL := "ws" + strings.TrimPrefix(a.AgentURL, "http") + "/resources/" + bid + "/desktop-stream?target=" + rid
	upstream, _, e := (&websocket.Dialer{HandshakeTimeout: 15 * time.Second}).DialContext(r.Context(), agentURL, http.Header{"Authorization": []string{"Bearer " + a.AgentToken}})
	if e != nil {
		_ = ws.WriteJSON(terminalFrame{Type: "error", Data: "Desktop gateway is unavailable"})
		return
	}
	defer upstream.Close()
	if e = upstream.WriteJSON(map[string]any{"password": secrets["password"], "width": auth.Width, "height": auth.Height}); e != nil {
		return
	}
	_ = ws.UnderlyingConn().SetDeadline(time.Time{})
	_ = upstream.UnderlyingConn().SetDeadline(time.Time{})
	_, _ = a.DB.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,'desktop.connect',$4)", id(), target.TenantID, uid, rid)
	var lastInput atomic.Int64
	lastInput.Store(time.Now().UnixNano())
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			kind, raw, e := ws.ReadMessage()
			if e != nil {
				return
			}
			if strings.Contains(string(raw), "3.key,") || strings.Contains(string(raw), "5.mouse,") {
				lastInput.Store(time.Now().UnixNano())
			}
			_ = upstream.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if e = upstream.WriteMessage(kind, raw); e != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			kind, raw, e := upstream.ReadMessage()
			if e != nil {
				return
			}
			_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if e = ws.WriteMessage(kind, raw); e != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	max := time.NewTimer(2 * time.Hour)
	defer max.Stop()
	for {
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case <-max.C:
			reason = "Maximum desktop duration reached"
			return
		case <-ticker.C:
			if time.Since(time.Unix(0, lastInput.Load())) > 15*time.Minute {
				reason = "Idle timeout"
				return
			}
			fresh, e := a.session(cookie.Value)
			if e != nil || fresh.ID != uid {
				reason = "Signed out or session expired"
				return
			}
			current, e := a.resource(rid)
			if e != nil || current.ContainerID != generation {
				reason = "Desktop changed"
				return
			}
			if e = a.desktopPermission(r.WithContext(context.WithValue(r.Context(), userKey{}, fresh)), current, bid); e != nil {
				reason = "Desktop access revoked"
				return
			}
			result, e := a.DB.Exec("UPDATE desktop_sessions SET last_seen=now() WHERE id=$1 AND state='Active'", sid)
			if e != nil {
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				return
			}
		}
	}
}
