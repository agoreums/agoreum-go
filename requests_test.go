package agoreum

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// Open requests (API 0.21.0): the right paths and bodies go out, and accepting
// a quote returns the order it created. Paths are built from the ids, as the
// API contract test reads literal paths in SDK sources.

const reqID, quoteID = "r1", "q1"

func TestPostRequestSendsOnlyWhatWasGiven(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/requests" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		writeJSON(w, 201, `{"id": "r1", "tags": ["fr"]}`)
	})
	if _, err := c.Requests.PostRequest(context.Background(), PostRequestParams{
		Title: "A translation", Brief: "Translate twenty words.", MaxBudget: "60", Tags: []string{"fr"},
	}); err != nil {
		t.Fatalf("PostRequest: %v", err)
	}
	if len(body) != 4 || body["max_budget"] != "60" {
		t.Fatalf("body %v, want title, brief, max_budget and tags only", body)
	}
}

func TestQuoteAndAcceptUseTheRequestPaths(t *testing.T) {
	var paths []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/requests/" + reqID + "/quotes":
			writeJSON(w, 201, `{"id": "q1", "total": "41.025641", "delivery_time_hours": 24}`)
		case "/requests/" + reqID + "/quotes/" + quoteID + "/accept":
			writeJSON(w, 201, `{"id": "o1", "reference": "AGO-ABC123", "status": "pending_payment"}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	q, err := c.Requests.QuoteRequest(context.Background(), reqID, QuoteRequestParams{
		ServiceID: "svc", Price: "40", DeliveryTimeHours: 24,
	})
	if err != nil || q.Total != "41.025641" {
		t.Fatalf("QuoteRequest: %v %+v", err, q)
	}
	order, err := c.Requests.AcceptQuote(context.Background(), reqID, quoteID, nil)
	if err != nil || order.Reference != "AGO-ABC123" {
		t.Fatalf("AcceptQuote: %v %+v", err, order)
	}
	if len(paths) != 2 || paths[0] != "POST /requests/"+reqID+"/quotes" ||
		paths[1] != "POST /requests/"+reqID+"/quotes/"+quoteID+"/accept" {
		t.Fatalf("paths %v", paths)
	}
}
