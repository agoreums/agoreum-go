package agoreum

// Waiting for an order: a step passed between polls still counts, leaving the
// path stops the wait at once, and the timeout says where the order was.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const waitOrderID = "66666666-6666-6666-6666-666666666666"

func waitServer(t *testing.T, statuses ...string) (*Client, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		status := statuses[len(statuses)-1]
		if calls < len(statuses) {
			status = statuses[calls]
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": waitOrderID, "reference": "AGO-1", "status": status})
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient("ak_test", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	return c, &calls
}

func TestATargetIsReachedAtItOrFurtherAlong(t *testing.T) {
	for _, c := range []struct {
		status, target string
		want           bool
	}{
		{"funded", "funded", true}, {"in_progress", "funded", true}, {"completed", "delivered", true},
		{"disputed", "funded", true}, {"pending_payment", "funded", false}, {"funded", "delivered", false},
	} {
		if got, err := Reached(waitOrderID, c.status, c.target); err != nil || got != c.want {
			t.Errorf("%s for %s: %v %v", c.status, c.target, got, err)
		}
	}
	var left *OrderLeftPath
	if _, err := Reached(waitOrderID, "expired", "funded"); !errors.As(err, &left) || left.Status != "expired" {
		t.Errorf("expired: %v", err)
	}
}

func TestWaitForStatusPollsUntilFunded(t *testing.T) {
	c, calls := waitServer(t, "pending_payment", "pending_payment", "in_progress")
	order, err := c.Orders.WaitForStatus(context.Background(), waitOrderID, "funded", 5*time.Second, 5*time.Millisecond)
	if err != nil || order.Status != "in_progress" || *calls != 3 {
		t.Fatalf("order %+v err %v calls %d", order, err, *calls)
	}
}

func TestWaitForStatusStopsWhenTheOrderExpires(t *testing.T) {
	c, calls := waitServer(t, "expired")
	_, err := c.Orders.WaitForStatus(context.Background(), waitOrderID, "funded", time.Minute, 5*time.Millisecond)
	var left *OrderLeftPath
	if !errors.As(err, &left) || *calls != 1 {
		t.Fatalf("err %v calls %d", err, *calls)
	}
}

func TestWaitForStatusTimesOutNamingWhereTheOrderWas(t *testing.T) {
	c, _ := waitServer(t, "pending_payment")
	_, err := c.Orders.WaitForStatus(context.Background(), waitOrderID, "funded", 40*time.Millisecond, 5*time.Millisecond)
	var timedOut *OrderWaitTimeout
	if !errors.As(err, &timedOut) || timedOut.Status != "pending_payment" {
		t.Fatalf("err %v", err)
	}
	if _, err := c.Orders.WaitForStatus(context.Background(), waitOrderID, "paid", 0, 0); err == nil {
		t.Fatal("an unknown status was accepted")
	}
}
