package platform

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const terminalSchema = `
CREATE TABLE IF NOT EXISTS resource_access_grants(user_id text REFERENCES users(id),resource_id text REFERENCES resources(id),capability text NOT NULL CHECK(capability='workspace.shell.connect'),granted_by text NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(user_id,resource_id,capability));
CREATE TABLE IF NOT EXISTS terminal_sessions(id text PRIMARY KEY,user_id text REFERENCES users(id),tenant_id text REFERENCES tenants(id),resource_id text REFERENCES resources(id),container_id text NOT NULL,ticket_hash text NOT NULL,auth_hash text NOT NULL,state text NOT NULL DEFAULT 'Pending',expires_at timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),started_at timestamptz,last_seen timestamptz NOT NULL DEFAULT now(),ended_at timestamptz,reason text NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS terminal_sessions_active ON terminal_sessions(resource_id,state);
`

type terminalFrame struct {
	Type   string `json:"type"`
	Ticket string `json:"ticket,omitempty"`
	Data   string `json:"data,omitempty"`
	Cols   int    `json:"cols,omitempty"`
	Rows   int    `json:"rows,omitempty"`
	Code   int    `json:"code"`
}

func terminalOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return allowedOrigin(origin)
}

func (a *App) shellAccess(r *http.Request, res Resource) error {
	if e := a.scope(r, res.TenantID, false); e != nil {
		return e
	}
	if !strings.HasPrefix(res.Type, "workspace-") {
		return bad("Browser shell is available for Linux workspaces")
	}
	u := actor(r)
	if u.Admin {
		return nil
	}
	role, e := a.role(u.ID, res.TenantID)
	if e != nil {
		return apiError{404, "resource not found"}
	}
	if role == "owner" {
		return nil
	}
	if role != "contributor" {
		return apiError{403, "Shell access requires an explicit connect grant"}
	}
	var n int
	if e = a.DB.QueryRow("SELECT count(*) FROM resource_access_grants WHERE user_id=$1 AND resource_id=$2 AND capability='workspace.shell.connect'", u.ID, res.ID).Scan(&n); e != nil {
		return e
	}
	if n == 0 {
		return apiError{403, "Ask a tenant owner to grant shell access to this workspace"}
	}
	return nil
}

func (a *App) terminalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/resources/{id}/shell-members", a.handle(a.shellMembers))
	mux.HandleFunc("POST /api/resources/{id}/terminal-sessions", a.handle(a.createTerminal))
	mux.HandleFunc("GET /api/terminal-sessions/{id}/stream", a.terminalStream)
	mux.HandleFunc("GET /api/resources/{id}/terminal-sessions", a.handle(a.listTerminals))
	mux.HandleFunc("DELETE /api/terminal-sessions/{id}", a.handle(a.endTerminal))
	mux.HandleFunc("PUT /api/resources/{id}/shell-access/{user}", a.handle(a.grantShell))
	mux.HandleFunc("DELETE /api/resources/{id}/shell-access/{user}", a.handle(a.grantShell))
	mux.HandleFunc("GET /api/resources/{id}/shell-access", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		res, e := a.resource(r.PathValue("id"))
		if e != nil {
			return e
		}
		if e = a.scope(r, res.TenantID, false); e != nil {
			return e
		}
		allowed := a.shellAccess(r, res) == nil
		role, _ := a.role(actor(r).ID, res.TenantID)
		jsonOut(w, 200, map[string]any{"canConnect": allowed, "canManage": actor(r).Admin || role == "owner", "user": "1000:1000", "directory": "/data", "idleMinutes": 15, "maxHours": 2})
		return nil
	}))
}

func (a *App) shellMembers(w http.ResponseWriter, r *http.Request) error {
	res, e := a.resource(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = a.scope(r, res.TenantID, false); e != nil {
		return e
	}
	role, _ := a.role(actor(r).ID, res.TenantID)
	if !actor(r).Admin && role != "owner" {
		return apiError{403, "Tenant owner permission required"}
	}
	rows, e := a.DB.Query("SELECT u.id,u.name,u.email,m.role,EXISTS(SELECT 1 FROM resource_access_grants g WHERE g.user_id=u.id AND g.resource_id=$2) FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.tenant_id=$1 ORDER BY u.name", res.TenantID, res.ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var uid, name, email, role string
		var granted bool
		if e = rows.Scan(&uid, &name, &email, &role, &granted); e != nil {
			return e
		}
		out = append(out, map[string]any{"id": uid, "name": name, "email": email, "role": role, "granted": granted})
	}
	jsonOut(w, 200, out)
	return rows.Err()
}

func (a *App) grantShell(w http.ResponseWriter, r *http.Request) error {
	res, e := a.resource(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = a.scope(r, res.TenantID, false); e != nil {
		return e
	}
	role, _ := a.role(actor(r).ID, res.TenantID)
	if !actor(r).Admin && role != "owner" {
		return apiError{403, "Tenant owner permission required"}
	}
	if !strings.HasPrefix(res.Type, "workspace-") {
		return bad("Only workspaces support shell access")
	}
	var targetRole string
	if e = a.DB.QueryRow("SELECT role FROM memberships WHERE user_id=$1 AND tenant_id=$2", r.PathValue("user"), res.TenantID).Scan(&targetRole); e != nil {
		return apiError{404, "tenant member not found"}
	}
	if targetRole == "reader" {
		return bad("Readers cannot receive interactive shell access")
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	action := "shell.access.grant"
	if r.Method == "DELETE" {
		action = "shell.access.revoke"
		_, e = tx.Exec("DELETE FROM resource_access_grants WHERE user_id=$1 AND resource_id=$2", r.PathValue("user"), res.ID)
		if e == nil {
			_, e = tx.Exec("UPDATE terminal_sessions SET state='Closed',ended_at=now(),reason='Access revoked' WHERE user_id=$1 AND resource_id=$2 AND state IN ('Pending','Active')", r.PathValue("user"), res.ID)
		}
	} else {
		_, e = tx.Exec("INSERT INTO resource_access_grants(user_id,resource_id,capability,granted_by) VALUES($1,$2,'workspace.shell.connect',$3) ON CONFLICT DO NOTHING", r.PathValue("user"), res.ID, actor(r).ID)
	}
	if e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,$4,$5)", id(), res.TenantID, actor(r).ID, action, res.ID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) createTerminal(w http.ResponseWriter, r *http.Request) error {
	res, e := a.resource(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = a.shellAccess(r, res); e != nil {
		return e
	}
	if res.State != "Running" || res.Desired != "Running" {
		return apiError{409, "Start the workspace before connecting"}
	}
	var observed Observation
	if e = a.agent(r.Context(), "GET", "/resources/"+res.ID+"/state", nil, &observed); e != nil {
		return apiError{503, "Node agent is unavailable"}
	}
	if observed.State != "Running" || observed.ContainerID != res.ContainerID {
		return apiError{409, "Workspace is changing; wait for it to become ready"}
	}
	cookie, _ := r.Cookie("kzure_session")
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("SELECT pg_advisory_xact_lock(7392102)"); e != nil {
		return e
	}
	// API crashes leave no permanent reservations. Active streams heartbeat every five seconds.
	if _, e = tx.Exec("UPDATE terminal_sessions SET state='Closed',ended_at=now(),reason='Session lease expired' WHERE (state='Pending' AND expires_at<now()) OR (state='Active' AND last_seen<now()-interval '30 seconds')"); e != nil {
		return e
	}
	var resourceN, userN, total int
	if e = tx.QueryRow("SELECT count(*) FILTER(WHERE resource_id=$1),count(*) FILTER(WHERE user_id=$2),count(*) FROM terminal_sessions WHERE state IN ('Pending','Active')", res.ID, actor(r).ID).Scan(&resourceN, &userN, &total); e != nil {
		return e
	}
	if resourceN >= 2 || userN >= 5 || total >= 32 {
		return apiError{429, "Interactive session limit reached; disconnect an existing session"}
	}
	sid, ticket := id(), token()
	if _, e = tx.Exec("INSERT INTO terminal_sessions(id,user_id,tenant_id,resource_id,container_id,ticket_hash,auth_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,now()+interval '30 seconds')", sid, actor(r).ID, res.TenantID, res.ID, res.ContainerID, hashToken(ticket), hashToken(cookie.Value)); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	jsonOut(w, 201, map[string]any{"id": sid, "ticket": ticket, "expiresIn": 30, "user": "1000:1000", "directory": "/data"})
	return nil
}

func (a *App) listTerminals(w http.ResponseWriter, r *http.Request) error {
	res, e := a.resource(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = a.scope(r, res.TenantID, false); e != nil {
		return e
	}
	role, _ := a.role(actor(r).ID, res.TenantID)
	rows, e := a.DB.Query("SELECT t.id,u.name,t.state,t.created_at,t.reason FROM terminal_sessions t JOIN users u ON u.id=t.user_id WHERE t.resource_id=$1 AND ($2 OR t.user_id=$3) ORDER BY t.created_at DESC LIMIT 30", res.ID, actor(r).Admin || role == "owner", actor(r).ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var sid, name, state, reason string
		var at time.Time
		if e = rows.Scan(&sid, &name, &state, &at, &reason); e != nil {
			return e
		}
		out = append(out, map[string]any{"id": sid, "user": name, "state": state, "createdAt": at, "reason": reason})
	}
	jsonOut(w, 200, out)
	return rows.Err()
}

func (a *App) endTerminal(w http.ResponseWriter, r *http.Request) error {
	var rid, uid string
	if e := a.DB.QueryRow("SELECT resource_id,user_id FROM terminal_sessions WHERE id=$1", r.PathValue("id")).Scan(&rid, &uid); e != nil {
		return e
	}
	res, e := a.resource(rid)
	if e != nil {
		return e
	}
	if e = a.scope(r, res.TenantID, false); e != nil {
		return e
	}
	role, _ := a.role(actor(r).ID, res.TenantID)
	if uid != actor(r).ID && !actor(r).Admin && role != "owner" {
		return apiError{403, "Session owner or tenant owner permission required"}
	}
	_, e = a.DB.Exec("UPDATE terminal_sessions SET state='Closed',ended_at=now(),reason='Disconnected by user' WHERE id=$1 AND state IN ('Pending','Active')", r.PathValue("id"))
	if e != nil {
		return e
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) closeTerminal(sid, tid, uid, rid, reason string) {
	// Preserve an explicit revocation/disconnect reason written by another request.
	_, _ = a.DB.Exec("UPDATE terminal_sessions SET state='Closed',ended_at=COALESCE(ended_at,now()),reason=CASE WHEN reason='' THEN $2 ELSE reason END WHERE id=$1", sid, reason)
	_, _ = a.DB.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,'shell.disconnect',$4)", id(), tid, uid, rid)
}

func (a *App) terminalStream(w http.ResponseWriter, r *http.Request) {
	if !terminalOrigin(r) {
		jsonOut(w, 403, map[string]string{"error": "Origin not allowed"})
		return
	}
	var rid, uid, container string
	e := a.DB.QueryRow("SELECT resource_id,user_id,container_id FROM terminal_sessions WHERE id=$1", r.PathValue("id")).Scan(&rid, &uid, &container)
	if e != nil || uid != actor(r).ID {
		jsonOut(w, 404, map[string]string{"error": "Session not found"})
		return
	}
	res, e := a.resource(rid)
	if e != nil {
		jsonOut(w, 404, map[string]string{"error": "Workspace not found"})
		return
	}
	if e = a.shellAccess(r, res); e != nil {
		a.handle(func(http.ResponseWriter, *http.Request) error { return e })(w, r)
		return
	}
	if res.State != "Running" || res.ContainerID != container {
		jsonOut(w, 409, map[string]string{"error": "Workspace is no longer ready"})
		return
	}
	ws, e := (&websocket.Upgrader{CheckOrigin: terminalOrigin, ReadBufferSize: 4096, WriteBufferSize: 16384}).Upgrade(w, r, nil)
	if e != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(65536)
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth terminalFrame
	if e = ws.ReadJSON(&auth); e != nil || auth.Type != "auth" || len(auth.Ticket) > 256 {
		return
	}
	cookie, _ := r.Cookie("kzure_session")
	var sid string
	e = a.DB.QueryRow("UPDATE terminal_sessions SET state='Active',started_at=now(),last_seen=now() WHERE id=$1 AND user_id=$2 AND ticket_hash=$3 AND auth_hash=$4 AND state='Pending' AND expires_at>now() RETURNING id", r.PathValue("id"), uid, hashToken(auth.Ticket), hashToken(cookie.Value)).Scan(&sid)
	if e != nil {
		_ = ws.WriteJSON(terminalFrame{Type: "error", Data: "Connection ticket expired or already used"})
		return
	}
	reason := "Connection closed"
	defer func() { a.closeTerminal(sid, res.TenantID, uid, rid, reason) }()
	_, _ = a.DB.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,'shell.connect',$4)", id(), res.TenantID, uid, rid)
	// The internal connection uses the existing agent bearer credential. The agent
	// verifies installation/resource/container identity before starting an exec.
	agentURL := "ws" + strings.TrimPrefix(a.AgentURL, "http") + "/resources/" + rid + "/terminal?session=" + sid + "&container=" + container
	hdr := http.Header{"Authorization": []string{"Bearer " + a.AgentToken}}
	upstream, _, e := (&websocket.Dialer{HandshakeTimeout: 10 * time.Second}).DialContext(r.Context(), agentURL, hdr)
	if e != nil {
		reason = "Node terminal unavailable"
		_ = ws.WriteJSON(terminalFrame{Type: "error", Data: reason})
		return
	}
	defer upstream.Close()
	// Clear inherited HTTP server deadlines after upgrading the connection.
	_ = ws.UnderlyingConn().SetDeadline(time.Time{})
	_ = upstream.UnderlyingConn().SetDeadline(time.Time{})
	var lastInput atomic.Int64
	lastInput.Store(time.Now().UnixNano())
	done := make(chan error, 2)
	go func() {
		for {
			var frame terminalFrame
			if err := ws.ReadJSON(&frame); err != nil {
				done <- err
				return
			}
			switch frame.Type {
			case "input":
				if len(frame.Data) > 32768 {
					done <- errors.New("Input too large")
					return
				}
				lastInput.Store(time.Now().UnixNano())
			case "resize":
				if !validTerminalSize(frame.Cols, frame.Rows) {
					continue
				}
			case "ack":
				// Output consumption must not reset the input idle timeout.
			default:
				continue
			}
			_ = upstream.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := upstream.WriteJSON(frame); err != nil {
				done <- err
				return
			}
		}
	}()
	go func() {
		for {
			kind, data, err := upstream.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err = ws.WriteMessage(kind, data); err != nil {
				done <- err
				return
			}
		}
	}()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	max := time.NewTimer(2 * time.Hour)
	defer max.Stop()
	for {
		select {
		case <-done:
			return
		case <-max.C:
			reason = "Maximum session duration reached"
			return
		case <-r.Context().Done():
			return
		case <-tick.C:
			if time.Since(time.Unix(0, lastInput.Load())) > 15*time.Minute {
				reason = "Idle timeout"
				return
			}
			active, e := a.session(cookie.Value)
			if e != nil || active.ID != uid {
				reason = "Signed out or session expired"
				return
			}
			current, e := a.resource(rid)
			if e != nil || current.State != "Running" || current.Desired != "Running" || current.ContainerID != container {
				reason = "Workspace stopped or changed"
				return
			}
			if e = a.shellAccess(r.WithContext(context.WithValue(r.Context(), userKey{}, active)), current); e != nil {
				reason = "Access revoked"
				return
			}
			result, e := a.DB.Exec("UPDATE terminal_sessions SET last_seen=now() WHERE id=$1 AND state='Active'", sid)
			if e != nil {
				reason = "Authorization unavailable"
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				reason = "Session terminated"
				return
			}
		}
	}
}

func validTerminalSize(cols, rows int) bool {
	return cols >= 2 && cols <= 500 && rows >= 2 && rows <= 500
}
