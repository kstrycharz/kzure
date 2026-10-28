package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

const backupSchema = `
CREATE TABLE IF NOT EXISTS workload_backups(id text PRIMARY KEY,tenant_id text NOT NULL,resource_id text REFERENCES resources(id),state text NOT NULL DEFAULT 'Queued',resource jsonb NOT NULL,secret_cipher text NOT NULL,key_cipher text NOT NULL,artifact text NOT NULL DEFAULT '',sha256 text NOT NULL DEFAULT '',bytes bigint NOT NULL DEFAULT 0,image text NOT NULL DEFAULT '',message text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS backup_restores(resource_id text PRIMARY KEY REFERENCES resources(id),backup_id text NOT NULL REFERENCES workload_backups(id),state text NOT NULL DEFAULT 'Pending');
`

type workloadBackup struct {
	ID         string    `json:"id"`
	ResourceID string    `json:"resourceId"`
	State      string    `json:"state"`
	SHA256     string    `json:"sha256"`
	Bytes      int64     `json:"bytes"`
	Image      string    `json:"image"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"createdAt"`
}
type restoreBackupKey struct{}
type restoreBackupContext struct{ ID, Cipher string }

func (a *App) backupOwner(r *http.Request, tenant string) error {
	if e := a.scope(r, tenant, true); e != nil {
		return e
	}
	if actor(r).Admin {
		return nil
	}
	role, e := a.role(actor(r).ID, tenant)
	if e != nil || role != "owner" {
		return apiError{403, "owner role required for workload recovery"}
	}
	return nil
}
func (a *App) backupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("DELETE /api/backups/{id}", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		tx, e := a.DB.Begin()
		if e != nil {
			return e
		}
		defer tx.Rollback()
		var tenant, state string
		var raw []byte
		if e = tx.QueryRow("SELECT tenant_id,state,resource FROM workload_backups WHERE id=$1 FOR UPDATE", r.PathValue("id")).Scan(&tenant, &state, &raw); e != nil {
			return e
		}
		if e = a.backupOwner(r, tenant); e != nil {
			return e
		}
		if state == "Queued" || state == "Running" {
			return apiError{409, "Wait for this backup to finish"}
		}
		var pending bool
		if e = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM backup_restores WHERE backup_id=$1 AND state='Pending')", r.PathValue("id")).Scan(&pending); e != nil {
			return e
		}
		if pending {
			return apiError{409, "Wait for active restores before deleting their backup"}
		}
		var source Resource
		if e = json.Unmarshal(raw, &source); e != nil {
			return e
		}
		var attempts int
		if e = tx.QueryRow("SELECT attempts FROM operations WHERE id=$1", r.PathValue("id")).Scan(&attempts); e != nil {
			return e
		}
		if attempts > 100 {
			return bad("Backup attempt inventory requires operator review")
		}
		for attempt := 1; attempt <= attempts; attempt++ {
			if e = a.agent(r.Context(), "POST", "/backups/delete", backupRequest{ProvisionRequest: ProvisionRequest{Resource: source}, Artifact: r.PathValue("id") + "-" + strconv.Itoa(attempt)}, nil); e != nil {
				return e
			}
		}
		if _, e = tx.Exec("UPDATE workload_backups SET state='Deleted',artifact='',key_cipher='',secret_cipher='',message='Archive permanently removed' WHERE id=$1", r.PathValue("id")); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		jsonOut(w, 200, map[string]bool{"deleted": true})
		return nil
	}))
	mux.HandleFunc("GET /api/resources/{id}/backups", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		res, e := a.resource(r.PathValue("id"))
		if e != nil {
			return e
		}
		if e = a.scope(r, res.TenantID, false); e != nil {
			return e
		}
		rows, e := a.DB.Query("SELECT id,resource_id,state,sha256,bytes,image,message,created_at FROM workload_backups WHERE resource_id=$1 ORDER BY created_at DESC", res.ID)
		if e != nil {
			return e
		}
		defer rows.Close()
		out := []workloadBackup{}
		for rows.Next() {
			var b workloadBackup
			if e = rows.Scan(&b.ID, &b.ResourceID, &b.State, &b.SHA256, &b.Bytes, &b.Image, &b.Message, &b.CreatedAt); e != nil {
				return e
			}
			out = append(out, b)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		jsonOut(w, 200, out)
		return nil
	}))
	mux.HandleFunc("POST /api/resources/{id}/backups", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		res, e := a.resource(r.PathValue("id"))
		if e != nil {
			return e
		}
		if e = a.backupOwner(r, res.TenantID); e != nil {
			return e
		}
		if !backupSupported(res) {
			return bad("This recovery path supports managed PostgreSQL, Valkey, S3, plain Linux workspaces and persistent container apps")
		}
		tx, e := a.DB.Begin()
		if e != nil {
			return e
		}
		defer tx.Rollback()
		var state, secret string
		if e = tx.QueryRow("SELECT state,secret_cipher FROM resources WHERE id=$1 FOR UPDATE", res.ID).Scan(&state, &secret); e != nil {
			return e
		}
		if state != "Running" && state != "Stopped" {
			return apiError{409, "Backup requires a running or stopped resource"}
		}
		var busy bool
		if e = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM operations WHERE resource_id=$1 AND state IN ('Queued','Running'))", res.ID).Scan(&busy); e != nil {
			return e
		}
		if busy {
			return apiError{409, "Wait for the current resource operation"}
		}
		// Re-read the snapshot under the row lock: configuration updates cannot race
		// the encrypted credential/specification that will accompany this archive.
		res, e = a.resource(res.ID)
		if e != nil {
			return e
		}
		key := make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return e
		}
		wrapped, e := a.seal(map[string]string{"key": base64.StdEncoding.EncodeToString(key)})
		if e != nil {
			return e
		}
		bid := id()
		snapshot, _ := json.Marshal(res)
		if _, e = tx.Exec("INSERT INTO workload_backups(id,tenant_id,resource_id,resource,secret_cipher,key_cipher) VALUES($1,$2,$3,$4,$5,$6)", bid, res.TenantID, res.ID, snapshot, secret, wrapped); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO operations(id,tenant_id,resource_id,action) VALUES($1,$2,$3,'backup')", bid, res.TenantID, res.ID); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO audit_events(id,tenant_id,user_id,action,resource_id) VALUES($1,$2,$3,'resource.backup',$4)", id(), res.TenantID, actor(r).ID, res.ID); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		jsonOut(w, 202, map[string]string{"backupId": bid, "operationId": bid})
		return nil
	}))
	mux.HandleFunc("POST /api/backups/{id}/restore", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Name    string `json:"name"`
			GroupID string `json:"groupId"`
		}
		if e := decode(r, &in); e != nil {
			return e
		}
		var tenant, state, cipher string
		var raw []byte
		e := a.DB.QueryRow("SELECT tenant_id,state,resource,secret_cipher FROM workload_backups WHERE id=$1", r.PathValue("id")).Scan(&tenant, &state, &raw, &cipher)
		if e != nil {
			return e
		}
		if e = a.backupOwner(r, tenant); e != nil {
			return e
		}
		if state != "Ready" {
			return apiError{409, "Only a completed backup can be restored"}
		}
		var source Resource
		if e = json.Unmarshal(raw, &source); e != nil {
			return e
		}
		source.Spec.ServiceBindings = nil
		source.Spec.SubnetID = ""
		if source.Type == "container-app" {
			sec, e := a.applicationSecrets(tenant, source.Type, source.Spec, map[string]string{})
			if e != nil {
				return e
			}
			cipher, e = a.seal(sec)
			if e != nil {
				return e
			}
		}
		body, _ := json.Marshal(map[string]any{"tenantId": tenant, "groupId": in.GroupID, "name": in.Name, "type": source.Type, "spec": source.Spec})
		request := r.Clone(context.WithValue(r.Context(), restoreBackupKey{}, restoreBackupContext{r.PathValue("id"), cipher}))
		request.Body = io.NopCloser(bytes.NewReader(body))
		return a.createResource(w, request)
	}))
}
func (a *App) workBackup(ctx context.Context, oid string, attempt int, res Resource) error {
	if _, e := a.DB.Exec("UPDATE workload_backups SET state='Running' WHERE id=$1 AND EXISTS(SELECT 1 FROM operations WHERE id=$1 AND attempts=$2 AND state='Running' AND lease_until>now())", oid, attempt); e != nil {
		return e
	}
	var raw []byte
	var wrapped string
	e := a.DB.QueryRow("SELECT resource,key_cipher FROM workload_backups WHERE id=$1", oid).Scan(&raw, &wrapped)
	var source Resource
	if e == nil {
		e = json.Unmarshal(raw, &source)
	}
	var key map[string]string
	if e == nil {
		key, e = a.unseal(wrapped)
	}
	result := backupResult{}
	if e == nil {
		e = a.agent(ctx, "POST", "/backup", backupRequest{ProvisionRequest: ProvisionRequest{Resource: source}, Artifact: oid + "-" + strconv.Itoa(attempt), Key: key["key"]}, &result)
	}
	if err := a.ownsOperation(ctx, oid, attempt); err != nil {
		return err
	}
	// Observe resumed compute even when archive creation failed. A backup failure
	// must not silently change the user's desired state or erase its data.
	var out Observation
	observeErr := a.agent(ctx, "GET", "/resources/"+res.ID+"/state", nil, &out)
	state, message, opState := "Ready", "Encrypted stopped-volume snapshot completed", "Succeeded"
	if e != nil {
		state, message, opState = "Failed", e.Error(), "Failed"
	}
	if observeErr != nil || out.State == "Missing" {
		if e == nil {
			e = errors.New("backup finished but source observation is unavailable")
		}
		state, message, opState = "Failed", e.Error(), "Failed"
		out.State = "Degraded"
	}
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var claimed int
	if err = tx.QueryRow("SELECT attempts FROM operations WHERE id=$1 AND state='Running' AND lease_until>now() FOR UPDATE", oid).Scan(&claimed); err != nil {
		return err
	}
	if claimed != attempt {
		return errors.New("backup operation superseded")
	}
	if _, err = tx.Exec("UPDATE workload_backups SET state=$2,artifact=$3,sha256=$4,bytes=$5,image=$6,message=$7 WHERE id=$1", oid, state, result.Artifact, result.SHA256, result.Bytes, result.Image, message); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE resources SET state=$2,error=$3,updated_at=now() WHERE id=$1", res.ID, out.State, func() string {
		if e != nil {
			return message
		}
		return ""
	}()); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE operations SET state=$2,message=$3,lease_until=NULL,updated_at=now() WHERE id=$1", oid, opState, message); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	_ = a.syncIngress()
	return e
}

func (a *App) restoreBackupRequest(r Resource) (backupRequest, error) {
	p := backupRequest{ProvisionRequest: ProvisionRequest{Resource: r, SSHKeys: a.authorizedKeys(r.ID)}}
	var wrapped, cipher string
	e := a.DB.QueryRow("SELECT b.artifact,b.sha256,b.image,b.key_cipher,r.secret_cipher FROM backup_restores x JOIN workload_backups b ON b.id=x.backup_id JOIN resources r ON r.id=x.resource_id WHERE x.resource_id=$1 AND b.state='Ready' AND x.state='Pending'", r.ID).Scan(&p.Artifact, &p.SHA256, &p.Image, &wrapped, &cipher)
	if e != nil {
		return p, e
	}
	key, e := a.unseal(wrapped)
	if e != nil {
		return p, e
	}
	p.Key = key["key"]
	p.Secrets, e = a.unseal(cipher)
	return p, e
}
