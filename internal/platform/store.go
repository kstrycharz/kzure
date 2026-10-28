package platform

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
	"time"
)

type DB struct{ *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS users(id text PRIMARY KEY,email text UNIQUE NOT NULL,name text NOT NULL,password text NOT NULL,admin boolean NOT NULL DEFAULT false);
CREATE TABLE IF NOT EXISTS sessions(hash text PRIMARY KEY,user_id text REFERENCES users(id),expires timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS tenants(id text PRIMARY KEY,name text NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS memberships(user_id text REFERENCES users(id),tenant_id text REFERENCES tenants(id),role text NOT NULL,PRIMARY KEY(user_id,tenant_id));
CREATE TABLE IF NOT EXISTS subscriptions(id text PRIMARY KEY,tenant_id text REFERENCES tenants(id),name text NOT NULL,cpu_limit numeric NOT NULL DEFAULT 20,ram_limit numeric NOT NULL DEFAULT 24,storage_limit numeric NOT NULL DEFAULT 500);
CREATE TABLE IF NOT EXISTS resource_groups(id text PRIMARY KEY,tenant_id text REFERENCES tenants(id),subscription_id text REFERENCES subscriptions(id),name text NOT NULL,UNIQUE(subscription_id,name));
CREATE TABLE IF NOT EXISTS resources(id text PRIMARY KEY,tenant_id text REFERENCES tenants(id),subscription_id text REFERENCES subscriptions(id),group_id text REFERENCES resource_groups(id),name text NOT NULL,type text NOT NULL,state text NOT NULL DEFAULT 'Pending',desired text NOT NULL DEFAULT 'Running',spec jsonb NOT NULL,secret_cipher text NOT NULL DEFAULT '',container_id text NOT NULL DEFAULT '',endpoint text NOT NULL DEFAULT '',volume text NOT NULL DEFAULT '',error text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),UNIQUE(group_id,name));
CREATE TABLE IF NOT EXISTS operations(id text PRIMARY KEY,tenant_id text NOT NULL,resource_id text REFERENCES resources(id),action text NOT NULL,state text NOT NULL DEFAULT 'Queued',message text NOT NULL DEFAULT 'Waiting for worker',lease_until timestamptz,attempts int NOT NULL DEFAULT 0,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS idempotency(user_id text NOT NULL,key text NOT NULL,body_hash text NOT NULL,resource_id text REFERENCES resources(id),PRIMARY KEY(user_id,key));
CREATE TABLE IF NOT EXISTS audit_events(id text PRIMARY KEY,tenant_id text NOT NULL,user_id text NOT NULL,action text NOT NULL,resource_id text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS allocations(id text PRIMARY KEY,resource_id text REFERENCES resources(id),cpu numeric NOT NULL,ram numeric NOT NULL,storage numeric NOT NULL,start_at timestamptz NOT NULL DEFAULT now(),end_at timestamptz);
CREATE INDEX IF NOT EXISTS operations_queue ON operations(state,created_at);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_allocation ON allocations(resource_id) WHERE end_at IS NULL;
CREATE INDEX IF NOT EXISTS resources_scope ON resources(tenant_id,state);
`

func OpenDB(url string) (DB, error) {
	db, e := sql.Open("pgx", url)
	if e != nil {
		return DB{}, e
	}
	db.SetMaxOpenConns(12)
	ctx, c := context.WithTimeout(context.Background(), 40*time.Second)
	defer c()
	for {
		e = db.PingContext(ctx)
		if e == nil {
			break
		}
		select {
		case <-ctx.Done():
			return DB{}, e
		case <-time.After(time.Second):
		}
	}
	return DB{db}, nil
}
func (a *App) Migrate() error {
	_, e := a.DB.Exec(schema + terminalSchema + remoteSchema + networkSchema + desktopSchema + policySchema + applicationSchema + ingressSchema + bindingSchema + backupSchema)
	if e != nil {
		return e
	}
	var n int
	if e = a.DB.QueryRow("SELECT count(*) FROM users").Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(requiredEnv("KZURE_ADMIN_PASSWORD")), 12)
	if e != nil {
		return e
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	uid, tid, sid, gid := id(), id(), id(), id()
	stmts := []struct {
		q string
		v []any
	}{
		{"INSERT INTO users(id,email,name,password,admin) VALUES($1,$2,'Local administrator',$3,true)", []any{uid, env("KZURE_ADMIN_EMAIL", "admin@kzure.local"), string(hash)}},
		{"INSERT INTO tenants(id,name) VALUES($1,'Home lab')", []any{tid}},
		{"INSERT INTO memberships VALUES($1,$2,'owner')", []any{uid, tid}},
		{"INSERT INTO subscriptions(id,tenant_id,name) VALUES($1,$2,'Development')", []any{sid, tid}},
		{"INSERT INTO resource_groups VALUES($1,$2,$3,'default')", []any{gid, tid, sid}},
	}
	for _, s := range stmts {
		if _, e = tx.Exec(s.q, s.v...); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func hashToken(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

const resourceSelect = `SELECT r.id,r.tenant_id,r.subscription_id,r.group_id,g.name,r.name,r.type,r.state,r.desired,r.spec,r.container_id,r.endpoint,r.error,r.created_at,r.updated_at,r.volume,r.revision,r.assigned_port,
COALESCE((SELECT SUM((a.cpu*.01+a.ram*.002+a.storage*.00014)*GREATEST(0,EXTRACT(EPOCH FROM (COALESCE(a.end_at,now())-a.start_at)))/3600) FROM allocations a WHERE a.resource_id=r.id),0)::float8
FROM resources r JOIN resource_groups g ON r.group_id=g.id `

type scanner interface{ Scan(...any) error }

func scanResource(row scanner) (Resource, error) {
	var r Resource
	var raw []byte
	e := row.Scan(&r.ID, &r.TenantID, &r.SubscriptionID, &r.GroupID, &r.GroupName, &r.Name, &r.Type, &r.State, &r.Desired, &raw, &r.ContainerID, &r.Endpoint, &r.Error, &r.CreatedAt, &r.UpdatedAt, &r.Volume, &r.Revision, &r.AssignedPort, &r.Accrued)
	if e != nil {
		return r, e
	}
	e = json.Unmarshal(raw, &r.Spec)
	r.MonthlyEstimate = monthly(r.Spec, r.Desired == "Running" && r.State != "Deleted")
	if r.State == "Deleted" {
		r.MonthlyEstimate = 0
	}
	return r, e
}
func (a *App) resource(rid string) (Resource, error) {
	return scanResource(a.DB.QueryRow(resourceSelect+" WHERE r.id=$1", rid))
}
func (a *App) resources(tid string) ([]Resource, error) {
	rows, e := a.DB.Query(resourceSelect+" WHERE r.tenant_id=$1 AND r.state<>'Deleted' ORDER BY r.created_at DESC", tid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Resource{}
	for rows.Next() {
		r, e := scanResource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (a *App) role(uid, tid string) (string, error) {
	var r string
	e := a.DB.QueryRow("SELECT role FROM memberships WHERE user_id=$1 AND tenant_id=$2", uid, tid).Scan(&r)
	return r, e
}
func (a *App) allocation(tx *sql.Tx, r Resource, running bool, storage bool) error {
	if _, e := tx.Exec("UPDATE allocations SET end_at=now() WHERE resource_id=$1 AND end_at IS NULL", r.ID); e != nil {
		return e
	}
	var cpu, ram, disk float64
	if running {
		cpu = r.Spec.CPU
		ram = r.Spec.RAM
	}
	if storage {
		disk = r.Spec.Storage
	}
	if cpu+ram+disk == 0 {
		return nil
	}
	_, e := tx.Exec("INSERT INTO allocations(id,resource_id,cpu,ram,storage) VALUES($1,$2,$3,$4,$5)", id(), r.ID, cpu, ram, disk)
	return e
}
func (a *App) session(raw string) (User, error) {
	var u User
	if raw == "" {
		return u, errors.New("missing session")
	}
	e := a.DB.QueryRow("SELECT u.id,u.email,u.name,u.admin FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.hash=$1 AND s.expires>now()", hashToken(raw)).Scan(&u.ID, &u.Email, &u.Name, &u.Admin)
	return u, e
}
