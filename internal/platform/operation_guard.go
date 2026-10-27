package platform

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type operationClaim struct {
	ID      string
	Attempt int
}
type operationClaimKey struct{}
type mutationCheckKey struct{}

func operationAllows(action, dispatch string) bool {
	switch dispatch {
	case "ensure":
		return action == "create" || action == "start" || action == "restore"
	case "replace":
		return action == "update"
	case "delete":
		return action == "delete" || action == "restore" // failed restore cleanup
	default:
		return action == dispatch && (action == "stop" || action == "restart" || action == "purge")
	}
}

func (a *App) operationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /internal/operations/{id}", a.handle(func(w http.ResponseWriter, r *http.Request) error {
		if len(a.AgentToken) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.AgentToken)) != 1 {
			return apiError{401, "Agent authorization required"}
		}
		attempt, err := strconv.Atoi(r.URL.Query().Get("attempt"))
		if err != nil || attempt < 1 {
			return bad("invalid operation attempt")
		}
		var action string
		err = a.DB.QueryRowContext(r.Context(), "SELECT action FROM operations WHERE id=$1 AND resource_id=$2 AND attempts=$3 AND state='Running' AND lease_until>now()", r.PathValue("id"), r.URL.Query().Get("resource"), attempt).Scan(&action)
		if err != nil || !operationAllows(action, r.URL.Query().Get("dispatch")) {
			return apiError{409, "Operation is no longer authorized to change this resource"}
		}
		jsonOut(w, 200, map[string]bool{"current": true})
		return nil
	}))
}

// Serialize lifecycle mutations for one resource while allowing unrelated
// resources, observations and firewall reconciliation to proceed independently.
type resourceGate struct {
	ready chan struct{}
	refs  int
}
type resourceGates struct {
	mu    sync.Mutex
	gates map[string]*resourceGate
}

func (g *resourceGates) acquire(ctx context.Context, rid string) (func(), error) {
	g.mu.Lock()
	if g.gates == nil {
		g.gates = map[string]*resourceGate{}
	}
	gate := g.gates[rid]
	if gate == nil {
		gate = &resourceGate{ready: make(chan struct{}, 1)}
		g.gates[rid] = gate
	}
	gate.refs++
	g.mu.Unlock()
	drop := func() {
		g.mu.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(g.gates, rid)
		}
		g.mu.Unlock()
	}
	select {
	case gate.ready <- struct{}{}:
		return func() { <-gate.ready; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

func (d *dockerClient) guardOperation(r *http.Request, rid, dispatch string) (*http.Request, func(), error) {
	claim := operationClaim{ID: r.Header.Get("X-Kzure-Operation")}
	claim.Attempt, _ = strconv.Atoi(r.Header.Get("X-Kzure-Attempt"))
	if len(claim.ID) != 32 || strings.Trim(claim.ID, "0123456789abcdef") != "" || claim.Attempt < 1 {
		return nil, nil, apiError{409, "A current operation attempt is required"}
	}
	release, err := d.resourceGates.acquire(r.Context(), rid)
	if err != nil {
		return nil, nil, err
	}
	check := func(ctx context.Context) error { return d.checkOperation(ctx, claim, rid, dispatch) }
	if err = check(r.Context()); err != nil {
		release()
		return nil, nil, apiError{409, err.Error()}
	}
	return r.WithContext(context.WithValue(r.Context(), mutationCheckKey{}, check)), release, nil
}

func (d *dockerClient) checkOperation(ctx context.Context, claim operationClaim, rid, dispatch string) error {
	query := url.Values{"resource": {rid}, "attempt": {strconv.Itoa(claim.Attempt)}, "dispatch": {dispatch}}
	req, err := http.NewRequestWithContext(ctx, "GET", d.controlURL+"/internal/operations/"+claim.ID+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.agentToken)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return errors.New("Operation ownership could not be verified")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Operation ownership rejected (%d)", response.StatusCode)
	}
	return nil
}

func checkMutation(ctx context.Context) error {
	if check, ok := ctx.Value(mutationCheckKey{}).(func(context.Context) error); ok {
		return check(ctx)
	}
	return nil
}
