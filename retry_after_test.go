package agoreum

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// A server asking for a long wait is answered with the rate-limit error rather
// than slept on: sleeping a minute or an hour inside one call looks exactly
// like a hang. A short wait is still honoured and retried.

func TestALongRetryAfterRaisesAtOnceCarryingIt(t *testing.T) {
	var calls int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "31")
		writeJSON(w, 429, `{"error":{"code":"rate_limited","message":"slow down"}}`)
	})
	c.maxRetries = 2

	started := time.Now()
	_, err := c.Me(context.Background())
	if !IsRateLimited(err) {
		t.Fatalf("want rate limited, got %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RetryAfter != 31 {
		t.Fatalf("RetryAfter not carried: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("calls = %d, want 1", n)
	}
	if time.Since(started) > 2*time.Second {
		t.Errorf("slept on a 31 second Retry-After")
	}
}

func TestTheLineIsThirtySecondsExactly(t *testing.T) {
	if waitsTooLong(30) || !waitsTooLong(30.001) || waitsTooLong(-1) {
		t.Fatalf("waitsTooLong boundary wrong: 30=%v 30.001=%v absent=%v", waitsTooLong(30), waitsTooLong(30.001), waitsTooLong(-1))
	}
}
