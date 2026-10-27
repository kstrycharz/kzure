package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const ingressSchema = `
CREATE TABLE IF NOT EXISTS application_endpoints(id text PRIMARY KEY,tenant_id text NOT NULL REFERENCES tenants(id),resource_id text NOT NULL UNIQUE REFERENCES resources(id),hostname text NOT NULL UNIQUE,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS ingress_status(id int PRIMARY KEY CHECK(id=1),state text NOT NULL DEFAULT 'Not configured',error text NOT NULL DEFAULT '',ca_pem text NOT NULL DEFAULT '',expires_at timestamptz,updated_at timestamptz NOT NULL DEFAULT now());
INSERT INTO ingress_status(id) VALUES(1) ON CONFLICT DO NOTHING;
`

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

type ingressTarget struct {
	Resource Resource `json:"resource"`
	Hostname string   `json:"hostname"`
}
type ingressRequest struct {
	HTTPPort  int             `json:"httpPort"`
	HTTPSPort int             `json:"httpsPort"`
	Targets   []ingressTarget `json:"targets"`
}

func ingressPorts() (int, int, error) {
	httpPort, a := strconv.Atoi(env("KZURE_APP_HTTP_PORT", "8089"))
	httpsPort, b := strconv.Atoi(env("KZURE_APP_HTTPS_PORT", "8444"))
	if a != nil || b != nil || httpPort < 1024 || httpPort > 65535 || httpsPort < 1024 || httpsPort > 65535 || httpPort == httpsPort {
		return 0, 0, errors.New("application gateway requires two distinct unprivileged host ports")
	}
	return httpPort, httpsPort, nil
}

func (a *App) ingressRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/resources/{id}/endpoint", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		res, err := a.resource(r.PathValue("id"))
		if err != nil {
			return err
		}
		if err = a.scope(r, res.TenantID, false); err != nil {
			return err
		}
		var hostname string
		err = a.DB.QueryRow("SELECT hostname FROM application_endpoints WHERE resource_id=$1", res.ID).Scan(&hostname)
		if err != nil && !isNoRows(err) {
			return err
		}
		var state, message string
		var expires *time.Time
		if err = a.DB.QueryRow("SELECT state,error,expires_at FROM ingress_status WHERE id=1").Scan(&state, &message, &expires); err != nil {
			return err
		}
		httpPort, httpsPort, err := ingressPorts()
		if err != nil {
			return err
		}
		address := ""
		if hostname != "" {
			address = fmt.Sprintf("https://%s:%d", hostname, httpsPort)
		}
		jsonOut(w, 200, map[string]any{"hostname": hostname, "url": address, "state": state, "error": message, "expiresAt": expires, "httpPort": httpPort, "httpsPort": httpsPort, "domain": applicationDomain, "scope": "This PC", "caUrl": "/api/application-endpoints/ca"})
		return nil
	}))
	mux.HandleFunc("POST /api/resources/{id}/endpoint", a.handle(a.saveApplicationEndpoint))
	mux.HandleFunc("DELETE /api/resources/{id}/endpoint", a.handle(a.saveApplicationEndpoint))
	mux.HandleFunc("GET /api/application-endpoints/ca", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		var ca string
		if err := a.DB.QueryRow("SELECT ca_pem FROM ingress_status WHERE id=1").Scan(&ca); err != nil {
			return err
		}
		if ca == "" {
			return apiError{404, "Create an application endpoint before downloading its certificate authority"}
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", `attachment; filename="kzure-applications-ca.crt"`)
		_, err := w.Write([]byte(ca))
		return err
	}))
}

func (a *App) saveApplicationEndpoint(w http.ResponseWriter, r *http.Request) error {
	res, err := a.resource(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err = a.scope(r, res.TenantID, true); err != nil {
		return err
	}
	if res.Type != "static-web" && (res.Type != "container-app" || res.Spec.Mode == "worker") {
		return bad("Managed HTTPS endpoints require a static site or HTTP container application")
	}
	if res.State != "Running" && r.Method != "DELETE" {
		return apiError{409, "Start the application before creating its endpoint"}
	}
	var in struct {
		Name string `json:"name"`
	}
	if r.Method != "DELETE" {
		if err = decode(r, &in); err != nil {
			return err
		}
		if in.Name == "" {
			in.Name = res.Name + "-" + res.ID[:8]
		}
		if !applicationLabel.MatchString(in.Name) || len(in.Name) > 63 {
			return bad("Use a 2–63 character lowercase application name containing letters, numbers and internal hyphens")
		}
	}
	return a.withIngressLock(func() error {
		hostname := ""
		if r.Method != "DELETE" {
			hostname = in.Name + "." + applicationDomain
		}
		old, err := a.setApplicationHostname(res, hostname, r.Method != "DELETE")
		if err != nil {
			return err
		}
		if err = a.syncIngressLocked(); err != nil {
			if _, restoreError := a.setApplicationHostname(res, old, false); restoreError != nil {
				return apiError{503, "Route application failed and metadata recovery needs attention: " + restoreError.Error()}
			}
			_ = a.syncIngressLocked()
			return apiError{503, "Application route could not be applied; previous route restored: " + err.Error()}
		}
		_, _ = a.DB.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,$4,$5)", id(), res.TenantID, actor(r).ID, "endpoint."+strings.ToLower(r.Method), res.ID)
		jsonOut(w, 200, map[string]bool{"saved": true})
		return nil
	})
}

func (a *App) setApplicationHostname(res Resource, hostname string, requireRunning bool) (string, error) {
	tx, err := a.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var state string
	if err = tx.QueryRow("SELECT state FROM resources WHERE id=$1 FOR UPDATE", res.ID).Scan(&state); err != nil {
		return "", err
	}
	if requireRunning && state != "Running" {
		return "", apiError{409, "Start the application before creating its endpoint"}
	}
	// Serialize metadata with lifecycle transitions. In particular a delayed
	// endpoint create/rollback cannot resurrect a hostname after a purge.
	if state == "Deleted" {
		hostname = ""
	}
	var old string
	if err = tx.QueryRow("SELECT hostname FROM application_endpoints WHERE resource_id=$1", res.ID).Scan(&old); err != nil && !isNoRows(err) {
		return "", err
	}
	if hostname == "" {
		_, err = tx.Exec("DELETE FROM application_endpoints WHERE resource_id=$1", res.ID)
	} else {
		_, err = tx.Exec("INSERT INTO application_endpoints(id,tenant_id,resource_id,hostname) VALUES($1,$2,$3,$4) ON CONFLICT(resource_id) DO UPDATE SET hostname=EXCLUDED.hostname", id(), res.TenantID, res.ID, hostname)
	}
	if err != nil {
		var constraint interface{ SQLState() string }
		if errors.As(err, &constraint) && constraint.SQLState() == "23505" {
			return "", apiError{409, "The application hostname is already in use"}
		}
		return "", err
	}
	return old, tx.Commit()
}

func (a *App) withIngressLock(fn func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	conn, err := a.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock(7392106)"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(7392106)")
	return fn()
}
func (a *App) syncIngress() error { return a.withIngressLock(a.syncIngressLocked) }
func (a *App) syncIngressLocked() error {
	var state string
	if err := a.DB.QueryRow("SELECT state FROM ingress_status WHERE id=1").Scan(&state); err != nil {
		return err
	}
	rows, err := a.DB.Query("SELECT resource_id,hostname FROM application_endpoints ORDER BY hostname")
	if err != nil {
		return err
	}
	targets := []ingressTarget{}
	for rows.Next() {
		var rid, hostname string
		if err = rows.Scan(&rid, &hostname); err != nil {
			rows.Close()
			return err
		}
		res, e := a.resource(rid)
		if e != nil {
			rows.Close()
			return e
		}
		targets = append(targets, ingressTarget{Resource: res, Hostname: hostname})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(targets) == 0 && state == "Not configured" {
		return nil
	}
	httpPort, httpsPort, err := ingressPorts()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var status edgeStatus
	err = a.agent(ctx, "POST", "/ingress", ingressRequest{HTTPPort: httpPort, HTTPSPort: httpsPort, Targets: targets}, &status)
	if err == nil {
		err = a.syncNetworkPolicies()
	}
	if err != nil {
		_, _ = a.DB.Exec("UPDATE ingress_status SET state='Degraded',error=$1,updated_at=now() WHERE id=1", err.Error())
		return err
	}
	_, err = a.DB.Exec("UPDATE ingress_status SET state='Applied',error='',ca_pem=$1,expires_at=$2,updated_at=now() WHERE id=1", status.CA, status.ExpiresAt)
	return err
}
