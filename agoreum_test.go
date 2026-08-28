package agoreum

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const meJSON = `{
  "id": "11111111-1111-1111-1111-111111111111",
  "username": "acme",
  "display_name": "Acme Labs",
  "primary_address": "0xf688A25DB028dE3FfC670c0C5A79ee1A5E9BD90A",
  "role": "user",
  "created_at": "2026-07-01T12:00:00Z",
  "auth": {"via_api_key": true, "scopes": ["marketplace:read", "orders:read"]}
}`

const servicePageJSON = `{
  "items": [{
    "id": "22222222-2222-2222-2222-222222222222",
    "slug": "fast-translation",
    "title": "Fast Translation",
    "summary": "Human-quality translation in minutes.",
    "pricing_model": "fixed",
    "price": "12.500000",
    "price_currency": "USDC",
    "price_unit": "per document",
    "delivery_time_hours": 24,
    "tags": ["translation", "localization"],
    "completed_order_count": 42,
    "review_count": 30,
    "average_rating": 4.8,
    "agent": {"id": "3", "slug": "acme-translate", "name": "Acme Translate", "verification_tier": "domain"}
  }],
  "total": 1, "limit": 20, "offset": 0, "query": "translation", "sort": "relevance"
}`

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := NewClient("ak_test", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestMe(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me" {
			t.Errorf("path = %q, want /me", r.URL.Path)
		}
		if got := r.Header.Get("X-API-Key"); got != "ak_test" {
			t.Errorf("X-API-Key = %q, want ak_test", got)
		}
		writeJSON(w, 200, meJSON)
	})

	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.Username == nil || *me.Username != "acme" {
		t.Errorf("username = %v, want acme", me.Username)
	}
	if scopes := me.Scopes(); len(scopes) != 2 || scopes[0] != "marketplace:read" {
		t.Errorf("scopes = %v", scopes)
	}
}

func TestSearchServices(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("q") != "translation" {
			t.Errorf("q = %q", q.Get("q"))
		}
		if tags := q["tags"]; len(tags) != 2 || tags[0] != "translation" {
			t.Errorf("tags = %v", tags)
		}
		if q.Get("min_rating") != "4" {
			t.Errorf("min_rating = %q, want 4", q.Get("min_rating"))
		}
		writeJSON(w, 200, servicePageJSON)
	})

	rating := 4.0
	page, err := c.Marketplace.SearchServices(context.Background(), SearchServicesParams{
		Query:     "translation",
		Tags:      []string{"translation", "localization"},
		MinRating: &rating,
	})
	if err != nil {
		t.Fatalf("SearchServices: %v", err)
	}
	if page.Total != 1 || page.HasMore() {
		t.Errorf("total=%d hasMore=%v", page.Total, page.HasMore())
	}
	svc := page.Items[0]
	if svc.Price == nil || *svc.Price != "12.500000" {
		t.Errorf("price = %v, want 12.500000", svc.Price)
	}
	if svc.Agent.Slug != "acme-translate" {
		t.Errorf("agent slug = %q", svc.Agent.Slug)
	}
}

func TestPlaceOrderOmitsNulls(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if _, ok := body["negotiated_price"]; ok {
			t.Errorf("negotiated_price should be omitted, got body %v", body)
		}
		if body["service_id"] != "svc-1" {
			t.Errorf("service_id = %v", body["service_id"])
		}
		writeJSON(w, 201, `{"id":"44","reference":"AGO-0001","status":"pending_payment","quantity":2,"unit_price":"12.5","subtotal":"25","platform_fee":"0.5","total_amount":"25.5","currency":"USDC","platform_fee_bps":200,"created_at":"2026-07-26T10:00:00Z","funding_deadline":null,"funded_at":null,"delivered_at":null,"auto_release_at":null,"completed_at":null}`)
	})

	order, err := c.Orders.Place(context.Background(), PlaceOrderParams{ServiceID: "svc-1", Quantity: 2})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if order.Reference != "AGO-0001" {
		t.Errorf("reference = %q", order.Reference)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		check  func(error) bool
	}{
		{"auth", 401, `{"error":{"code":"unauthenticated","message":"Provide a key."}}`, IsAuthError},
		{"not_found", 404, `{"error":{"code":"not_found","message":"No such agent."}}`, IsNotFound},
		{"scope", 403, `{"error":{"code":"insufficient_scope","message":"missing","details":{"missing":["orders:read"]}}}`, IsInsufficientScope},
		{"server", 500, `{"error":{"code":"internal_error","message":"boom"}}`, IsServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, tc.status, tc.body)
			})
			_, err := c.Agents.Get(context.Background(), "x")
			if err == nil {
				t.Fatal("expected error")
			}
			if !tc.check(err) {
				t.Errorf("predicate failed for %v", err)
			}
			var apiErr *APIError
			if !asErr(err, &apiErr) {
				t.Fatalf("not an *APIError: %v", err)
			}
			if apiErr.StatusCode != tc.status {
				t.Errorf("status = %d, want %d", apiErr.StatusCode, tc.status)
			}
		})
	}
}

func TestInsufficientScopeDetails(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 403, `{"error":{"code":"insufficient_scope","message":"missing","details":{"missing":["orders:read"]}}}`)
	})
	_, err := c.Orders.List(context.Background())
	var apiErr *APIError
	if !asErr(err, &apiErr) {
		t.Fatalf("not an *APIError: %v", err)
	}
	missing, _ := apiErr.Details["missing"].([]any)
	if len(missing) != 1 || missing[0] != "orders:read" {
		t.Errorf("details.missing = %v", apiErr.Details["missing"])
	}
}

func TestRetriesThenSucceeds(t *testing.T) {
	var calls int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			writeJSON(w, 429, `{"error":{"code":"rate_limited","message":"slow down"}}`)
			return
		}
		writeJSON(w, 200, meJSON)
	})
	c.maxRetries = 2

	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if me.Username == nil {
		t.Error("username nil")
	}
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	var calls int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "0")
		writeJSON(w, 429, `{"error":{"code":"rate_limited","message":"slow down"}}`)
	})
	c.maxRetries = 1

	_, err := c.Me(context.Background())
	if !IsRateLimited(err) {
		t.Fatalf("want rate limited, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 { // initial + 1 retry
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestNewClientRequiresKey(t *testing.T) {
	if _, err := NewClient(""); err == nil {
		t.Error("expected error for empty api key")
	}
}

// asErr is a tiny errors.As wrapper kept local to the test to keep call sites terse.
func asErr(err error, target **APIError) bool {
	e, ok := asAPIError(err)
	if ok {
		*target = e
	}
	return ok
}
