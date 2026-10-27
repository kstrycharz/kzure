package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResourceGateCancellationAndIsolation(t *testing.T) {
	var gates resourceGates
	release, err := gates.acquire(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	other, err := gates.acquire(context.Background(), "second")
	if err != nil {
		t.Fatal(err)
	}
	other()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := gates.acquire(ctx, "first"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("same-resource mutation was not serialized")
	}
	release()
	if len(gates.gates) != 0 {
		t.Fatal("resource gates leaked after release/cancellation")
	}
}

func TestAgentRejectsStaleClaimAndRechecksBeforeMutation(t *testing.T) {
	var current atomic.Bool
	current.Store(true)
	var checks atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture" || r.URL.Query().Get("resource") != "resource" || r.URL.Query().Get("attempt") != "2" || r.URL.Query().Get("dispatch") != "ensure" {
			t.Error("missing operation scope in ownership check")
		}
		if !current.Load() {
			w.WriteHeader(http.StatusConflict)
		}
	}))
	defer api.Close()
	d := &dockerClient{controlURL: api.URL, agentToken: "fixture"}
	r := httptest.NewRequest("POST", "/ensure", nil)
	if _, _, err := d.guardOperation(r, "resource", "ensure"); err == nil {
		t.Fatal("unclaimed mutation accepted")
	}
	r.Header.Set("X-Kzure-Operation", strings.Repeat("ab", 16))
	r.Header.Set("X-Kzure-Attempt", "2")
	guarded, release, err := d.guardOperation(r, "resource", "ensure")
	if err != nil {
		t.Fatal(err)
	}
	current.Store(false)
	// No Docker transport is configured: rejection must happen before dispatch.
	if _, err = d.call(guarded.Context(), "DELETE", "/containers/example", nil); err == nil {
		t.Fatal("lost operation claim still mutated Docker")
	}
	if err = d.archive(guarded.Context(), "example", "/tmp", "config", nil, 0600); err == nil {
		t.Fatal("lost operation claim still wrote container files")
	}
	release()
	if _, _, err = d.guardOperation(r, "resource", "ensure"); err == nil {
		t.Fatal("delayed stale request accepted after gate release")
	}
	if checks.Load() != 4 || len(d.resourceGates.gates) != 0 {
		t.Fatal("ownership checks or gate cleanup missing")
	}
}

func TestOperationDispatchCannotEscalateToPurge(t *testing.T) {
	for _, action := range []string{"create", "restore", "start", "update", "stop", "delete", "restart"} {
		if operationAllows(action, "purge") {
			t.Fatal("non-purge operation authorized data destruction")
		}
	}
	if !operationAllows("restore", "delete") || !operationAllows("update", "replace") || !operationAllows("purge", "purge") || operationAllows("stop", "ensure") {
		t.Fatal("incorrect lifecycle dispatch scope")
	}
}
