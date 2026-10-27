package platform

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

func (a *App) agent(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, a.AgentURL+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+a.AgentToken)
	req.Header.Set("Content-Type", "application/json")
	res, e := (&http.Client{Timeout: 9 * time.Minute}).Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, 4*1024*1024))
	if e != nil {
		return e
	}
	if res.StatusCode >= 400 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &v)
		return fmt.Errorf("node agent: %s", v.Error)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
func (a *App) RunWorker() error {
	log.Print("Resource worker started")
	if e := a.reconcileNetworks(); e != nil {
		log.Printf("network recovery: %v", e)
	}
	lastReconcile := time.Now().Add(-time.Minute)
	for {
		if e := a.workOne(); e != nil {
			log.Printf("worker: %v", e)
		}
		if time.Since(lastReconcile) > 20*time.Second {
			if e := a.reconcile(); e != nil {
				log.Printf("reconcile: %v", e)
			}
			lastReconcile = time.Now()
			a.reconcileRemote()
			a.reconcileConnections()
			if e := a.syncNetworkPolicies(); e != nil {
				log.Printf("network policy: %v", e)
			}
		}
		time.Sleep(time.Second)
	}
}

func (a *App) reconcile() error {
	rows, e := a.DB.Query(`SELECT r.id FROM resources r WHERE r.state IN ('Running','Stopped','Degraded') AND NOT EXISTS(SELECT 1 FROM operations o WHERE o.resource_id=r.id AND o.state IN ('Queued','Running'))`)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var rid string
		if e = rows.Scan(&rid); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, rid)
	}
	rows.Close()
	for _, rid := range ids {
		res, e := a.resource(rid)
		if e != nil {
			return e
		}
		var out Observation
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		e = a.agent(ctx, "GET", "/resources/"+rid+"/state", nil, &out)
		cancel()
		if e != nil {
			continue
		}
		action := ""
		if res.Desired == "Running" && (out.State == "Missing" || out.State == "Stopped") {
			action = "start"
		}
		if res.Desired == "Stopped" && out.State == "Running" {
			action = "stop"
		}
		tx, e := a.DB.Begin()
		if e != nil {
			return e
		}
		if _, e = tx.Exec("SELECT pg_advisory_xact_lock(7392101)"); e != nil {
			tx.Rollback()
			return e
		}
		var currentDesired string
		if e = tx.QueryRow("SELECT desired FROM resources WHERE id=$1 FOR UPDATE", rid).Scan(&currentDesired); e != nil {
			tx.Rollback()
			return e
		}
		var active int
		if e = tx.QueryRow("SELECT count(*) FROM operations WHERE resource_id=$1 AND state IN ('Queued','Running')", rid).Scan(&active); e != nil {
			tx.Rollback()
			return e
		}
		if active > 0 || currentDesired != res.Desired {
			tx.Rollback()
			continue
		}
		if action != "" {
			_, e = tx.Exec("INSERT INTO operations(id,tenant_id,resource_id,action,message) VALUES($1,$2,$3,$4,'Desired-state reconciliation')", id(), res.TenantID, rid, action)
		} else if out.State != "Missing" {
			_, e = tx.Exec("UPDATE resources SET state=$1,container_id=$2,endpoint=$3,volume=$4 WHERE id=$5", out.State, out.ContainerID, out.Endpoint, out.Volume, rid)
		}
		if e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) workOne() error {
	var oid, rid, action string
	var attempt int
	e := a.DB.QueryRow(`UPDATE operations SET state='Running',attempts=attempts+1,lease_until=now()+interval '10 minutes',updated_at=now(),message='Provisioning on local Docker host' WHERE id=(SELECT id FROM operations WHERE state='Queued' OR (state='Running' AND lease_until<now()) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,resource_id,action,attempts`).Scan(&oid, &rid, &action, &attempt)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	res, e := a.resource(rid)
	if e != nil {
		return e
	}
	_, e = a.DB.Exec("UPDATE resources SET state=$1,error='',updated_at=now() WHERE id=$2", map[string]string{"create": "Provisioning", "start": "Provisioning", "restart": "Updating", "stop": "Updating", "delete": "Deleting", "purge": "Deleting"}[action], rid)
	if e != nil {
		return e
	}
	var out Observation
	if action == "create" || action == "start" {
		var encrypted string
		if e = a.DB.QueryRow("SELECT secret_cipher FROM resources WHERE id=$1", rid).Scan(&encrypted); e == nil {
			var sec map[string]string
			sec, e = a.unseal(encrypted)
			if e == nil {
				var network *NetworkPlan
				network, e = a.networkFor(res)
				if e == nil {
					if network != nil {
						e = a.agent(context.Background(), "POST", "/networks", map[string]string{"id": network.ID, "tenantId": res.TenantID, "cidr": network.CIDR}, nil)
					}
					if e == nil {
						e = a.syncNetworkPolicies()
					}
				}
				if e == nil {
					e = a.agent(context.Background(), "POST", "/ensure", ProvisionRequest{Resource: res, Secrets: sec, Network: network, SSHKeys: a.authorizedKeys(res.ID)}, &out)
				}
			}
		}
	} else {
		e = a.agent(context.Background(), "POST", "/resources/"+rid+"/actions", map[string]string{"action": action}, &out)
	}
	if e != nil {
		message := e.Error()
		if len(message) > 1800 {
			message = message[:1800]
		}
		tx, err := a.DB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.Exec("UPDATE operations SET state='Failed',message=$1,updated_at=now(),lease_until=NULL WHERE id=$2 AND attempts=$3", message, oid, attempt); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE resources SET state='Failed',error=$1,updated_at=now() WHERE id=$2", message, rid); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return e
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var currentAttempt int
	if e = tx.QueryRow("SELECT attempts FROM operations WHERE id=$1 FOR UPDATE", oid).Scan(&currentAttempt); e != nil {
		return e
	}
	if currentAttempt != attempt {
		return errors.New("operation lease superseded")
	}
	desired := "Running"
	if out.State == "Stopped" {
		desired = "Stopped"
	}
	if out.State == "Deleted" || out.State == "Retained" {
		desired = "Deleted"
	}
	if _, e = tx.Exec("UPDATE resources SET state=$1,desired=$2,container_id=$3,endpoint=$4,volume=$5,error='',updated_at=now() WHERE id=$6", out.State, desired, out.ContainerID, out.Endpoint, out.Volume, rid); e != nil {
		return e
	}
	if e = a.allocation(tx, res, out.State == "Running" || out.State == "Degraded", out.Volume != ""); e != nil {
		return e
	}
	if out.State == "Deleted" {
		if _, e = tx.Exec("DELETE FROM ip_allocations WHERE resource_id=$1", rid); e != nil {
			return e
		}
	}
	message := "Resource is ready"
	switch action {
	case "stop":
		message = "Compute stopped; persistent data retained"
	case "delete":
		message = "Compute deleted"
		if out.Volume != "" {
			message = "Compute deleted; data volume retained and continues storage showback"
		}
	case "purge":
		message = "Resource and its managed data have been removed"
	}
	if _, e = tx.Exec("UPDATE operations SET state='Succeeded',message=$1,updated_at=now(),lease_until=NULL WHERE id=$2", message, oid); e != nil {
		return e
	}
	return tx.Commit()
}
