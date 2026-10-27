package platform

import (
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ResetAdmin is an operator-only CLI action. It is never exposed over HTTP.
// The password comes from the existing private installation environment.
func (a *App) ResetAdmin() error {
	email := strings.ToLower(strings.TrimSpace(requiredEnv("KZURE_ADMIN_EMAIL")))
	password := requiredEnv("KZURE_ADMIN_PASSWORD")
	if len(password) < 12 || len(password) > 72 {
		return errors.New("administrator password must be 12–72 bytes")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e != nil {
		return e
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var uid string
	if e = tx.QueryRow("UPDATE users SET password=$1 WHERE email=$2 AND admin=true RETURNING id", string(hash), email).Scan(&uid); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM sessions WHERE user_id=$1", uid); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE terminal_sessions SET state='Closed',ended_at=now(),reason='Administrator credentials reset' WHERE user_id=$1 AND state IN ('Pending','Active')", uid); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE desktop_sessions SET state='Closed',ended_at=now(),reason='Administrator credentials reset' WHERE user_id=$1 AND state IN ('Pending','Active')", uid); e != nil {
		return e
	}
	return tx.Commit()
}
