package platform

import (
	"context"
	"errors"
	"time"
)

var errOperationSuperseded = errors.New("operation lease superseded")

// Keep the lease alive independently of slow agent calls. Losing ownership or
// losing contact with the database cancels dispatch and readiness polling. The
// database completion transaction remains the authority for recording results.
func keepOperationLease(parent context.Context, interval time.Duration, renew func(context.Context) error) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check, stop := context.WithTimeout(ctx, 5*time.Second)
				err := renew(check)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); <-done }
}

func (a *App) renewOperation(ctx context.Context, oid string, attempt int) error {
	result, err := a.DB.ExecContext(ctx, "UPDATE operations SET lease_until=now()+interval '10 minutes' WHERE id=$1 AND attempts=$2 AND state='Running' AND lease_until>now()", oid, attempt)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errOperationSuperseded
	}
	return nil
}

func (a *App) ownsOperation(ctx context.Context, oid string, attempt int) error {
	var owns bool
	if err := a.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM operations WHERE id=$1 AND attempts=$2 AND state='Running' AND lease_until>now())", oid, attempt).Scan(&owns); err != nil {
		return err
	}
	if !owns {
		return errOperationSuperseded
	}
	return nil
}

func (a *App) markOperationResource(ctx context.Context, oid, rid string, attempt int, state string) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	if err = tx.QueryRowContext(ctx, "SELECT attempts FROM operations WHERE id=$1 AND resource_id=$2 AND state='Running' AND lease_until>now() FOR UPDATE", oid, rid).Scan(&current); err != nil {
		return err
	}
	if current != attempt {
		return errOperationSuperseded
	}
	if _, err = tx.ExecContext(ctx, "UPDATE resources SET state=$1,error='',updated_at=now() WHERE id=$2", state, rid); err != nil {
		return err
	}
	return tx.Commit()
}
