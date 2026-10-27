package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type apiError struct {
	Code    int
	Message string
}

func (e apiError) Error() string { return e.Message }
func bad(s string) error         { return apiError{400, s} }
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return bad("invalid JSON: " + e.Error())
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return bad("request must contain one JSON object")
	}
	return nil
}

type userKey struct{}

func actor(r *http.Request) User { return r.Context().Value(userKey{}).(User) }
func (a *App) scope(r *http.Request, tid string, write bool) error {
	if tid == "" {
		return bad("select a tenant")
	}
	u := actor(r)
	if u.Admin {
		return nil
	}
	role, e := a.role(u.ID, tid)
	if e != nil {
		return apiError{404, "tenant not found"}
	}
	if write && role == "reader" {
		return apiError{403, "contributor or owner role required"}
	}
	return nil
}
func (a *App) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if e := fn(w, r); e != nil {
			var ae apiError
			if errors.As(e, &ae) {
				jsonOut(w, ae.Code, map[string]string{"error": ae.Message})
			} else if errors.Is(e, sql.ErrNoRows) {
				jsonOut(w, 404, map[string]string{"error": "resource not found"})
			} else {
				log.Printf("request %s %s failed: %v", r.Method, r.URL.Path, e)
				jsonOut(w, 500, map[string]string{"error": "The request could not be completed. Check platform logs."})
			}
		}
	}
}
func (a *App) RunAPI() error {
	mux := http.NewServeMux()
	a.operationRoutes(mux)
	var mu sync.Mutex
	attempts := map[string][]time.Time{}
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		if e := a.DB.PingContext(r.Context()); e != nil {
			jsonOut(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		jsonOut(w, 200, map[string]string{"status": "ok", "version": "0.1.0"})
	})
	mux.HandleFunc("POST /api/login", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		mu.Lock()
		recent := []time.Time{}
		remote, _, _ := net.SplitHostPort(r.RemoteAddr)
		for _, t := range attempts[remote] {
			if time.Since(t) < time.Minute {
				recent = append(recent, t)
			}
		}
		limited := len(recent) >= 10
		attempts[remote] = append(recent, time.Now())
		if len(attempts) > 10000 {
			attempts = map[string][]time.Time{}
		}
		mu.Unlock()
		if limited {
			return apiError{429, "too many sign-in attempts; try again in one minute"}
		}
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if e := decode(r, &in); e != nil {
			return e
		}
		if len(in.Password) > 72 || len(in.Email) > 254 {
			return bad("invalid credentials")
		}
		var u User
		var hash string
		e := a.DB.QueryRow("SELECT id,email,name,admin,password FROM users WHERE email=$1", strings.ToLower(in.Email)).Scan(&u.ID, &u.Email, &u.Name, &u.Admin, &hash)
		if e != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
			return apiError{401, "Email or password is incorrect"}
		}
		raw := token()
		_, e = a.DB.Exec("INSERT INTO sessions(hash,user_id,expires) VALUES($1,$2,now()+interval '12 hours')", hashToken(raw), u.ID)
		if e != nil {
			return e
		}
		http.SetCookie(w, &http.Cookie{Name: "kzure_session", Value: raw, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureCookie(r), MaxAge: 43200})
		jsonOut(w, 200, u)
		return nil
	}))
	secured := http.NewServeMux()
	a.terminalRoutes(secured)
	a.remoteRoutes(secured, mux)
	a.networkRoutes(secured)
	a.desktopRoutes(secured)
	a.applicationRoutes(secured)
	a.networkPolicyRoutes(secured)
	secured.HandleFunc("GET /api/me", a.handle(func(w http.ResponseWriter, r *http.Request) error { jsonOut(w, 200, actor(r)); return nil }))
	secured.HandleFunc("POST /api/logout", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		if c, e := r.Cookie("kzure_session"); e == nil {
			_, _ = a.DB.Exec("DELETE FROM sessions WHERE hash=$1", hashToken(c.Value))
		}
		http.SetCookie(w, &http.Cookie{Name: "kzure_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		jsonOut(w, 200, map[string]bool{"ok": true})
		return nil
	}))
	secured.HandleFunc("GET /api/catalog", a.handle(func(w http.ResponseWriter, r *http.Request) error { jsonOut(w, 200, Catalog); return nil }))
	secured.HandleFunc("GET /api/host", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		var h Host
		if e := a.agent(r.Context(), "GET", "/host", nil, &h); e != nil {
			return apiError{503, "Node agent is unavailable"}
		}
		var cpu, ram, storage float64
		e := a.DB.QueryRow("SELECT COALESCE(SUM(CASE WHEN desired='Running' AND state NOT IN ('Deleted','Retained') THEN (spec->>'cpu')::numeric ELSE 0 END),0),COALESCE(SUM(CASE WHEN desired='Running' AND state NOT IN ('Deleted','Retained') THEN (spec->>'ram')::numeric ELSE 0 END),0),COALESCE(SUM(CASE WHEN state<>'Deleted' THEN (spec->>'storage')::numeric ELSE 0 END),0) FROM resources").Scan(&cpu, &ram, &storage)
		if e != nil {
			return e
		}
		jsonOut(w, 200, map[string]any{"host": h, "allocatedCpu": cpu, "allocatedRam": ram, "allocatedStorage": storage, "storageNote": "Declared capacity only; Docker Desktop volume quotas are not enforced."})
		return nil
	}))
	secured.HandleFunc("GET /api/tenants", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		u := actor(r)
		q := "SELECT t.id,t.name,t.created_at FROM tenants t"
		args := []any{}
		if !u.Admin {
			q += " JOIN memberships m ON m.tenant_id=t.id WHERE m.user_id=$1"
			args = append(args, u.ID)
		}
		q += " ORDER BY t.created_at"
		rows, e := a.DB.Query(q, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, name string
			var created time.Time
			if e = rows.Scan(&id, &name, &created); e != nil {
				return e
			}
			out = append(out, map[string]any{"id": id, "name": name, "createdAt": created})
		}
		jsonOut(w, 200, out)
		return rows.Err()
	}))
	secured.HandleFunc("POST /api/tenants", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		if !actor(r).Admin {
			return apiError{403, "installation administrator required"}
		}
		var in struct {
			Name string `json:"name"`
		}
		if e := decode(r, &in); e != nil {
			return e
		}
		if len(strings.TrimSpace(in.Name)) < 2 || len(in.Name) > 80 {
			return bad("tenant name must be 2–80 characters")
		}
		tid, sid, gid := id(), id(), id()
		tx, e := a.DB.Begin()
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.Exec("INSERT INTO tenants(id,name) VALUES($1,$2)", tid, in.Name); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO memberships VALUES($1,$2,'owner')", actor(r).ID, tid); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO subscriptions(id,tenant_id,name) VALUES($1,$2,'Development')", sid, tid); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO resource_groups VALUES($1,$2,$3,'default')", gid, tid, sid); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		jsonOut(w, 201, map[string]string{"id": tid, "name": in.Name})
		return nil
	}))
	secured.HandleFunc("GET /api/subscriptions", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		tid := r.URL.Query().Get("tenant")
		if e := a.scope(r, tid, false); e != nil {
			return e
		}
		rows, e := a.DB.Query("SELECT id,name,cpu_limit::float8,ram_limit::float8,storage_limit::float8 FROM subscriptions WHERE tenant_id=$1 ORDER BY name", tid)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, name string
			var cpu, ram, disk float64
			if e = rows.Scan(&id, &name, &cpu, &ram, &disk); e != nil {
				return e
			}
			out = append(out, map[string]any{"id": id, "name": name, "cpuLimit": cpu, "ramLimit": ram, "storageLimit": disk})
		}
		jsonOut(w, 200, out)
		return rows.Err()
	}))
	secured.HandleFunc("POST /api/subscriptions", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			TenantID string `json:"tenantId"`
			Name     string `json:"name"`
		}
		if e := decode(r, &in); e != nil {
			return e
		}
		if e := a.scope(r, in.TenantID, true); e != nil {
			return e
		}
		if !actor(r).Admin {
			role, _ := a.role(actor(r).ID, in.TenantID)
			if role != "owner" {
				return apiError{403, "tenant owner required"}
			}
		}
		if len(strings.TrimSpace(in.Name)) < 2 || len(in.Name) > 80 {
			return bad("name must be 2–80 characters")
		}
		sid := id()
		if _, e := a.DB.Exec("INSERT INTO subscriptions(id,tenant_id,name) VALUES($1,$2,$3)", sid, in.TenantID, in.Name); e != nil {
			return e
		}
		jsonOut(w, 201, map[string]string{"id": sid, "name": in.Name})
		return nil
	}))
	secured.HandleFunc("GET /api/groups", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		tid := r.URL.Query().Get("tenant")
		if e := a.scope(r, tid, false); e != nil {
			return e
		}
		rows, e := a.DB.Query("SELECT g.id,g.name,g.subscription_id,s.name,(SELECT count(*) FROM resources r WHERE r.group_id=g.id AND r.state<>'Deleted') FROM resource_groups g JOIN subscriptions s ON s.id=g.subscription_id WHERE g.tenant_id=$1 ORDER BY g.name", tid)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, name, sid, sname string
			var count int
			if e = rows.Scan(&id, &name, &sid, &sname, &count); e != nil {
				return e
			}
			out = append(out, map[string]any{"id": id, "name": name, "subscriptionId": sid, "subscriptionName": sname, "resources": count})
		}
		jsonOut(w, 200, out)
		return rows.Err()
	}))
	secured.HandleFunc("POST /api/groups", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			TenantID       string `json:"tenantId"`
			SubscriptionID string `json:"subscriptionId"`
			Name           string `json:"name"`
		}
		if e := decode(r, &in); e != nil {
			return e
		}
		if e := a.scope(r, in.TenantID, true); e != nil {
			return e
		}
		if !validName.MatchString(in.Name) {
			return bad("use a lowercase group name, 2–48 characters")
		}
		var n int
		if e := a.DB.QueryRow("SELECT count(*) FROM subscriptions WHERE id=$1 AND tenant_id=$2", in.SubscriptionID, in.TenantID).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return bad("subscription does not belong to this tenant")
		}
		gid := id()
		if _, e := a.DB.Exec("INSERT INTO resource_groups VALUES($1,$2,$3,$4)", gid, in.TenantID, in.SubscriptionID, in.Name); e != nil {
			return apiError{409, "group name already exists in this subscription"}
		}
		jsonOut(w, 201, map[string]string{"id": gid, "name": in.Name})
		return nil
	}))
	secured.HandleFunc("GET /api/resources", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		tid := r.URL.Query().Get("tenant")
		if e := a.scope(r, tid, false); e != nil {
			return e
		}
		out, e := a.resources(tid)
		if e != nil {
			return e
		}
		jsonOut(w, 200, out)
		return nil
	}))
	secured.HandleFunc("POST /api/resources", a.handle(a.createResource))
	secured.HandleFunc("GET /api/resources/{id}", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		res, e := a.resource(r.PathValue("id"))
		if e != nil {
			return e
		}
		if e = a.scope(r, res.TenantID, false); e != nil {
			return e
		}
		jsonOut(w, 200, res)
		return nil
	}))
	secured.HandleFunc("POST /api/resources/{id}/actions", a.handle(a.resourceAction))
	secured.HandleFunc("GET /api/resources/{id}/logs", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		res, e := a.resource(r.PathValue("id"))
		if e != nil {
			return e
		}
		if e = a.scope(r, res.TenantID, false); e != nil {
			return e
		}
		var out map[string]string
		if e = a.agent(r.Context(), "GET", "/resources/"+res.ID+"/logs", nil, &out); e != nil {
			return apiError{502, e.Error()}
		}
		if res.Type == "container-app" {
			out["logs"], e = a.redactApplicationOutput(res.ID, out["logs"])
			if e != nil {
				return e
			}
		}
		jsonOut(w, 200, out)
		return nil
	}))
	secured.HandleFunc("GET /api/resources/{id}/metrics", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		res, e := a.resource(r.PathValue("id"))
		if e != nil {
			return e
		}
		if e = a.scope(r, res.TenantID, false); e != nil {
			return e
		}
		var out Observation
		if e = a.agent(r.Context(), "GET", "/resources/"+res.ID+"/observe", nil, &out); e != nil {
			return apiError{502, e.Error()}
		}
		jsonOut(w, 200, out)
		return nil
	}))
	secured.HandleFunc("GET /api/resources/{id}/credentials", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		res, e := a.resource(r.PathValue("id"))
		if e != nil {
			return e
		}
		if e = a.scope(r, res.TenantID, false); e != nil {
			return e
		}
		if res.Type == "container-app" {
			return apiError{403, "Application secrets are supplied through saved references and are not revealed by this endpoint"}
		}
		if res.Type == "bastion" || isSSHWorkspace(res.Type) {
			return apiError{403, "Host identity and gateway control keys are managed by the platform"}
		}
		u := actor(r)
		role, _ := a.role(u.ID, res.TenantID)
		if !u.Admin && role != "owner" {
			return apiError{403, "owner role required to reveal credentials"}
		}
		var cipher string
		if e = a.DB.QueryRow("SELECT secret_cipher FROM resources WHERE id=$1", res.ID).Scan(&cipher); e != nil {
			return e
		}
		out, e := a.unseal(cipher)
		if e != nil {
			return e
		}
		_, _ = a.DB.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,'credentials.reveal',$4)", id(), res.TenantID, u.ID, res.ID)
		jsonOut(w, 200, out)
		return nil
	}))
	secured.HandleFunc("GET /api/operations", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		tid := r.URL.Query().Get("tenant")
		if e := a.scope(r, tid, false); e != nil {
			return e
		}
		rows, e := a.DB.Query("SELECT o.id,o.resource_id,r.name,o.action,o.state,o.message,o.created_at,o.updated_at FROM operations o JOIN resources r ON r.id=o.resource_id WHERE o.tenant_id=$1 ORDER BY o.created_at DESC LIMIT 100", tid)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []Operation{}
		for rows.Next() {
			var o Operation
			if e = rows.Scan(&o.ID, &o.ResourceID, &o.ResourceName, &o.Action, &o.State, &o.Message, &o.CreatedAt, &o.UpdatedAt); e != nil {
				return e
			}
			out = append(out, o)
		}
		jsonOut(w, 200, out)
		return rows.Err()
	}))
	secured.HandleFunc("GET /api/cost", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		tid := r.URL.Query().Get("tenant")
		if e := a.scope(r, tid, false); e != nil {
			return e
		}
		rows, e := a.DB.Query(resourceSelect+" WHERE r.tenant_id=$1 ORDER BY r.created_at DESC", tid)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []Resource{}
		var accrued, forecast float64
		for rows.Next() {
			res, e := scanResource(rows)
			if e != nil {
				return e
			}
			accrued += res.Accrued
			forecast += res.MonthlyEstimate
			out = append(out, res)
		}
		jsonOut(w, 200, map[string]any{"resources": out, "accrued": accrued, "monthlyEstimate": forecast, "currency": "USD", "rates": map[string]float64{"cpu": .01, "ram": .002, "storage": .00014}, "basis": "Illustrative allocated-capacity showback. Storage is declared, not hard-quota enforced on this host."})
		return rows.Err()
	}))
	secured.HandleFunc("POST /api/users", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		if !actor(r).Admin {
			return apiError{403, "installation administrator required"}
		}
		var in struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Password string `json:"password"`
			TenantID string `json:"tenantId"`
			Role     string `json:"role"`
		}
		if e := decode(r, &in); e != nil {
			return e
		}
		if len(in.Password) < 12 || len(in.Password) > 72 || !strings.Contains(in.Email, "@") || len(in.Name) > 80 {
			return bad("provide an email and a 12–72 character password")
		}
		if in.Role != "owner" && in.Role != "contributor" && in.Role != "reader" {
			return bad("role must be owner, contributor or reader")
		}
		h, e := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
		if e != nil {
			return e
		}
		uid := id()
		tx, e := a.DB.Begin()
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.Exec("INSERT INTO users(id,email,name,password) VALUES($1,$2,$3,$4)", uid, strings.ToLower(in.Email), in.Name, string(h)); e != nil {
			return apiError{409, "email already exists"}
		}
		if _, e = tx.Exec("INSERT INTO memberships VALUES($1,$2,$3)", uid, in.TenantID, in.Role); e != nil {
			return bad("invalid tenant")
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		jsonOut(w, 201, map[string]string{"id": uid})
		return nil
	}))
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie("kzure_session")
		if e != nil {
			jsonOut(w, 401, map[string]string{"error": "Sign in to continue"})
			return
		}
		u, e := a.session(c.Value)
		if e != nil {
			jsonOut(w, 401, map[string]string{"error": "Session expired; sign in again"})
			return
		}
		secured.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	}))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != "GET" && r.Method != "HEAD" {
			if r.Header.Get("X-Kzure-Request") != "portal" {
				jsonOut(w, 403, map[string]string{"error": "X-Kzure-Request header required"})
				return
			}
			origin := r.Header.Get("Origin")
			if origin != "" && !allowedOrigin(origin) {
				jsonOut(w, 403, map[string]string{"error": "Origin not allowed"})
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024)
		mux.ServeHTTP(w, r)
	})
	log.Printf("Management API listening on :8090")
	return (&http.Server{Addr: ":8090", Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 10 * time.Minute, IdleTimeout: 60 * time.Second}).ListenAndServe()
}

func (a *App) createResource(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		TenantID string `json:"tenantId"`
		GroupID  string `json:"groupId"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		Spec     Spec   `json:"spec"`
	}
	if e := decode(r, &in); e != nil {
		return e
	}
	if e := a.scope(r, in.TenantID, true); e != nil {
		return e
	}
	if e := validateSpec(in.Type, in.Name, in.Spec); e != nil {
		return bad(e.Error())
	}
	if e := a.validateAppReferences(r, in.TenantID, in.Type, in.Spec); e != nil {
		return e
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return bad("idempotency key too long")
	}
	if key == "" {
		return bad("Idempotency-Key header required")
	}
	raw, _ := json.Marshal(in)
	bodyHash := hashToken(string(raw))
	u := actor(r)
	var h Host
	if e := a.agent(r.Context(), "GET", "/host", nil, &h); e != nil {
		return apiError{503, "Node agent is unavailable"}
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("SELECT pg_advisory_xact_lock(7392101)"); e != nil {
		return e
	}
	var existing, existingHash string
	e = tx.QueryRow("SELECT resource_id,body_hash FROM idempotency WHERE user_id=$1 AND key=$2", u.ID, key).Scan(&existing, &existingHash)
	if e == nil {
		if existingHash != bodyHash {
			return apiError{409, "idempotency key was already used for a different request"}
		}
		res, e := a.resource(existing)
		if e != nil {
			return e
		}
		jsonOut(w, 200, res)
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	var sid string
	var cpuLimit, ramLimit, diskLimit float64
	e = tx.QueryRow("SELECT s.id,s.cpu_limit::float8,s.ram_limit::float8,s.storage_limit::float8 FROM subscriptions s JOIN resource_groups g ON g.subscription_id=s.id WHERE g.id=$1 AND g.tenant_id=$2", in.GroupID, in.TenantID).Scan(&sid, &cpuLimit, &ramLimit, &diskLimit)
	if e != nil {
		return bad("resource group does not belong to this tenant")
	}
	var cpu, ram, subCPU, subRAM, disk float64
	e = tx.QueryRow("SELECT COALESCE(SUM((spec->>'cpu')::numeric),0),COALESCE(SUM((spec->>'ram')::numeric),0) FROM resources WHERE desired='Running' AND state NOT IN ('Deleted','Retained')").Scan(&cpu, &ram)
	if e != nil {
		return e
	}
	if cpu+in.Spec.CPU > float64(h.CPUs)-h.ReservedCPU || ram+in.Spec.RAM > h.RAM-h.ReservedRAM {
		return apiError{409, "Insufficient host capacity after platform reservations"}
	}
	e = tx.QueryRow("SELECT COALESCE(SUM(CASE WHEN desired='Running' THEN (spec->>'cpu')::numeric ELSE 0 END),0),COALESCE(SUM(CASE WHEN desired='Running' THEN (spec->>'ram')::numeric ELSE 0 END),0),COALESCE(SUM((spec->>'storage')::numeric),0) FROM resources WHERE subscription_id=$1 AND state<>'Deleted'", sid).Scan(&subCPU, &subRAM, &disk)
	if e != nil {
		return e
	}
	if subCPU+in.Spec.CPU > cpuLimit || subRAM+in.Spec.RAM > ramLimit || disk+in.Spec.Storage > diskLimit {
		return apiError{409, "Subscription quota would be exceeded"}
	}
	rid, oid := id(), id()
	spec, _ := json.Marshal(in.Spec)
	secretValues, e := a.applicationSecrets(in.TenantID, in.Type, in.Spec, secretsFor(in.Type))
	if e != nil {
		return e
	}
	cipher, e := a.seal(secretValues)
	if e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO resources(id,tenant_id,subscription_id,group_id,name,type,spec,secret_cipher) VALUES($1,$2,$3,$4,$5,$6,$7,$8)", rid, in.TenantID, sid, in.GroupID, in.Name, in.Type, spec, cipher); e != nil {
		return apiError{409, "A resource with this name already exists in the group"}
	}
	if in.Type == "container-app" {
		if _, e = tx.Exec("INSERT INTO resource_revisions(resource_id,revision,spec,secret_cipher,state) VALUES($1,1,$2,$3,'Pending')", rid, spec, cipher); e != nil {
			return e
		}
		if in.Spec.Mode != "worker" {
			if _, e = tx.Exec("UPDATE resources SET assigned_port=(SELECT n FROM generate_series(20000,29999) n WHERE NOT EXISTS(SELECT 1 FROM resources WHERE assigned_port=n AND state<>'Deleted') ORDER BY n LIMIT 1) WHERE id=$1", rid); e != nil {
				return e
			}
		}
	}
	if _, e = a.reserveNetwork(tx, rid, in.TenantID, in.Spec); e != nil {
		return e
	}
	for _, s := range []struct {
		q    string
		args []any
	}{
		{"INSERT INTO operations(id,tenant_id,resource_id,action) VALUES($1,$2,$3,'create')", []any{oid, in.TenantID, rid}},
		{"INSERT INTO idempotency VALUES($1,$2,$3,$4)", []any{u.ID, key, bodyHash, rid}},
		{"INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,'resource.create',$4)", []any{id(), in.TenantID, u.ID, rid}},
	} {
		if _, e = tx.Exec(s.q, s.args...); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	res, e := a.resource(rid)
	if e != nil {
		return e
	}
	jsonOut(w, 202, map[string]any{"resource": res, "operationId": oid})
	return nil
}
func (a *App) resourceAction(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Action string `json:"action"`
	}
	if e := decode(r, &in); e != nil {
		return e
	}
	if in.Action != "restore" && in.Action != "start" && in.Action != "stop" && in.Action != "restart" && in.Action != "delete" && in.Action != "purge" {
		return bad("unsupported action")
	}
	res, e := a.resource(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = a.scope(r, res.TenantID, true); e != nil {
		return e
	}
	if in.Action == "purge" && !actor(r).Admin {
		role, _ := a.role(actor(r).ID, res.TenantID)
		if role != "owner" {
			return apiError{403, "owner role required to permanently purge managed data"}
		}
	}
	if res.State == "Deleted" {
		return apiError{409, "resource was already deleted"}
	}
	if in.Action == "delete" || in.Action == "purge" {
		var dependencies int
		if e = a.DB.QueryRow("SELECT (SELECT count(*) FROM service_connections WHERE source_id=$1 OR target_id=$1)+(SELECT count(*) FROM bastion_targets WHERE bastion_id=$1 OR target_id=$1)", res.ID).Scan(&dependencies); e != nil {
			return e
		}
		if dependencies > 0 {
			return apiError{409, "Remove service connections and bastion target bindings before deleting this resource"}
		}
	}
	if res.State == "Retained" && in.Action != "purge" && in.Action != "restore" {
		return apiError{409, "restore or purge the retained data"}
	}
	if in.Action == "restore" && res.State != "Retained" {
		return apiError{409, "Only retained resources can be restored"}
	}
	var h Host
	if in.Action == "start" || in.Action == "restart" || in.Action == "restore" {
		if e = a.agent(r.Context(), "GET", "/host", nil, &h); e != nil {
			return apiError{503, "Node agent is unavailable"}
		}
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("SELECT pg_advisory_xact_lock(7392101)"); e != nil {
		return e
	}
	var desired, state string
	if e = tx.QueryRow("SELECT desired,state FROM resources WHERE id=$1 FOR UPDATE", res.ID).Scan(&desired, &state); e != nil {
		return e
	}
	if state == "Deleted" || (state == "Retained" && in.Action != "purge" && in.Action != "restore") || in.Action == "restore" && state != "Retained" {
		return apiError{409, "resource is deleted; retained data supports purge only"}
	}
	var busy int
	if e = tx.QueryRow("SELECT count(*) FROM operations WHERE resource_id=$1 AND state IN ('Queued','Running')", res.ID).Scan(&busy); e != nil {
		return e
	}
	if busy > 0 {
		return apiError{409, "another operation is already in progress"}
	}
	if (in.Action == "start" || in.Action == "restart" || in.Action == "restore") && desired != "Running" {
		var cpu, ram, sc, sr float64
		if e = tx.QueryRow("SELECT COALESCE(SUM((spec->>'cpu')::numeric),0),COALESCE(SUM((spec->>'ram')::numeric),0) FROM resources WHERE desired='Running' AND state NOT IN ('Deleted','Retained')").Scan(&cpu, &ram); e != nil {
			return e
		}
		if cpu+res.Spec.CPU > float64(h.CPUs)-h.ReservedCPU || ram+res.Spec.RAM > h.RAM-h.ReservedRAM {
			return apiError{409, "Insufficient host capacity"}
		}
		if e = tx.QueryRow("SELECT COALESCE(SUM((spec->>'cpu')::numeric),0),COALESCE(SUM((spec->>'ram')::numeric),0) FROM resources WHERE desired='Running' AND subscription_id=$1 AND state NOT IN ('Deleted','Retained')", res.SubscriptionID).Scan(&cpu, &ram); e != nil {
			return e
		}
		if e = tx.QueryRow("SELECT cpu_limit::float8,ram_limit::float8 FROM subscriptions WHERE id=$1", res.SubscriptionID).Scan(&sc, &sr); e != nil {
			return e
		}
		if cpu+res.Spec.CPU > sc || ram+res.Spec.RAM > sr {
			return apiError{409, "Subscription quota would be exceeded"}
		}
	}
	next := desired
	if in.Action == "start" || in.Action == "restart" || in.Action == "restore" {
		next = "Running"
	}
	// Keep running reservations until the worker confirms deallocation.
	oid := id()
	if _, e = tx.Exec("INSERT INTO operations(id,tenant_id,resource_id,action) VALUES($1,$2,$3,$4)", oid, res.TenantID, res.ID, in.Action); e != nil {
		return e
	}
	queuedState := map[string]string{"restore": "Provisioning", "start": "Provisioning", "stop": "Updating", "restart": "Updating", "delete": "Deleting", "purge": "Deleting"}[in.Action]
	if _, e = tx.Exec("UPDATE resources SET desired=$1,state=$2,updated_at=now() WHERE id=$3", next, queuedState, res.ID); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,$4,$5)", id(), res.TenantID, actor(r).ID, "resource."+in.Action, res.ID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	jsonOut(w, 202, map[string]string{"operationId": oid, "message": fmt.Sprintf("%s queued", in.Action)})
	return nil
}
