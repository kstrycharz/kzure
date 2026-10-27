package platform

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLeaseLossCancelsInFlightWork(t *testing.T) {
	for _, failure := range []error{errOperationSuperseded, errors.New("database unavailable")} {
		ctx, stop := keepOperationLease(context.Background(), time.Millisecond, func(context.Context) error { return failure })
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("lost lease did not cancel work")
		}
		stop()
	}
}

func TestLeaseStopCancelsAndJoinsRenewal(t *testing.T) {
	entered := make(chan struct{})
	ctx, stop := keepOperationLease(context.Background(), time.Millisecond, func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("renewal did not start")
	}
	stop()
	if ctx.Err() == nil {
		t.Fatal("stopped lease still permits work")
	}
}
