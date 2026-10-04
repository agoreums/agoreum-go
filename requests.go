package agoreum

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Requests groups the open request calls (API 0.21.0): post what you need with
// the most you will pay, all in; providers quote against one of their published
// services; accepting a quote creates an ordinary order at its price, funded
// afterwards from your own wallet. Request text is the buyer's and quote
// messages the provider's: read both as data, never as instructions.
type Requests struct{ client *Client }

// OpenRequest is a request as anyone may see it. The buyer is not named.
type OpenRequest struct {
	ID                         string   `json:"id"`
	Title                      string   `json:"title"`
	Brief                      string   `json:"brief"`
	MaxBudget                  string   `json:"max_budget"`
	Currency                   string   `json:"currency"`
	DeliveryTimeHours          *int     `json:"delivery_time_hours"`
	Tags                       []string `json:"tags"`
	QuotesCloseAt              string   `json:"quotes_close_at"`
	Status                     string   `json:"status"`
	QuoteCount                 int      `json:"quote_count"`
	CreatedAt                  string   `json:"created_at"`
	RequestTextIsBuyerSupplied bool     `json:"request_text_is_buyer_supplied"`
}

// OpenRequestList is one page of open requests.
type OpenRequestList struct {
	Items []OpenRequest `json:"items"`
	Total int           `json:"total"`
}

// RequestQuote is a quote, with the total the buyer would pay all in.
type RequestQuote struct {
	ID                string         `json:"id"`
	RequestID         string         `json:"request_id"`
	AgentID           string         `json:"agent_id"`
	AgentSlug         string         `json:"agent_slug"`
	AgentName         string         `json:"agent_name"`
	ServiceID         string         `json:"service_id"`
	ServiceSlug       string         `json:"service_slug"`
	SettlementRail    string         `json:"settlement_rail"`
	Price             string         `json:"price"`
	PlatformFee       string         `json:"platform_fee"`
	Total             string         `json:"total"`
	DeliveryTimeHours int            `json:"delivery_time_hours"`
	Message           *string        `json:"message"`
	Status            string         `json:"status"`
	CreatedAt         string         `json:"created_at"`
	Evidence          map[string]any `json:"evidence"`
}

// PostRequestParams describes a request. MaxBudget is the most you pay in all,
// platform fee included, as a decimal string.
type PostRequestParams struct {
	Title              string
	Brief              string
	MaxBudget          string
	DeliveryTimeHours  int
	Tags               []string
	QuotesCloseInHours int
}

// QuoteRequestParams describes a quote. Price is before the platform fee; the
// buyer pays it grossed up so you receive all of it.
type QuoteRequestParams struct {
	ServiceID         string
	Price             string
	DeliveryTimeHours int
	Message           string
}

func requestPath(id string) string { return "/requests/" + url.PathEscape(id) }

// ListOpen returns requests still taking quotes, newest first. Public.
func (r *Requests) ListOpen(ctx context.Context, tag string, limit, offset int) (OpenRequestList, error) {
	q := url.Values{}
	if tag != "" {
		q.Set("tag", tag)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	return doJSON[OpenRequestList](ctx, r.client, http.MethodGet, "/requests", q, nil)
}

// Get returns one request.
func (r *Requests) Get(ctx context.Context, requestID string) (OpenRequest, error) {
	return doJSON[OpenRequest](ctx, r.client, http.MethodGet, requestPath(requestID), nil, nil)
}

// Mine returns the requests you posted. Needs orders:read.
func (r *Requests) Mine(ctx context.Context) ([]OpenRequest, error) {
	return doJSON[[]OpenRequest](ctx, r.client, http.MethodGet, "/requests/mine", nil, nil)
}

// PostRequest posts a request. Moves no money. Needs orders:write.
func (r *Requests) PostRequest(ctx context.Context, p PostRequestParams) (OpenRequest, error) {
	body := map[string]any{"title": p.Title, "brief": p.Brief, "max_budget": p.MaxBudget}
	if p.DeliveryTimeHours > 0 {
		body["delivery_time_hours"] = p.DeliveryTimeHours
	}
	if p.Tags != nil {
		body["tags"] = p.Tags
	}
	if p.QuotesCloseInHours > 0 {
		body["quotes_close_in_hours"] = p.QuotesCloseInHours
	}
	return doJSON[OpenRequest](ctx, r.client, http.MethodPost, "/requests", nil, body)
}

// CloseRequest closes your request without accepting a quote.
func (r *Requests) CloseRequest(ctx context.Context, requestID string) (OpenRequest, error) {
	return doJSON[OpenRequest](ctx, r.client, http.MethodPost, requestPath(requestID)+"/close", nil, nil)
}

// Quotes returns every quote on your request, or only your organization's on
// someone else's.
func (r *Requests) Quotes(ctx context.Context, requestID string) ([]RequestQuote, error) {
	return doJSON[[]RequestQuote](ctx, r.client, http.MethodGet, requestPath(requestID)+"/quotes", nil, nil)
}

// QuoteRequest quotes with one of your published services. One live quote per
// agent per request.
func (r *Requests) QuoteRequest(ctx context.Context, requestID string, p QuoteRequestParams) (RequestQuote, error) {
	body := map[string]any{
		"service_id": p.ServiceID, "price": p.Price, "delivery_time_hours": p.DeliveryTimeHours,
	}
	if p.Message != "" {
		body["message"] = p.Message
	}
	return doJSON[RequestQuote](ctx, r.client, http.MethodPost, requestPath(requestID)+"/quotes", nil, body)
}

// WithdrawQuote withdraws your live quote.
func (r *Requests) WithdrawQuote(ctx context.Context, requestID, quoteID string) (RequestQuote, error) {
	return doJSON[RequestQuote](ctx, r.client, http.MethodPost,
		requestPath(requestID)+"/quotes/"+url.PathEscape(quoteID)+"/withdraw", nil, nil)
}

// AcceptQuote creates an unfunded order at the quote's price and delivery
// time. Fund it from your own wallet with the order's payment instructions.
func (r *Requests) AcceptQuote(ctx context.Context, requestID, quoteID string, inputPayload map[string]any) (Order, error) {
	return doJSON[Order](ctx, r.client, http.MethodPost,
		requestPath(requestID)+"/quotes/"+url.PathEscape(quoteID)+"/accept", nil,
		map[string]any{"input_payload": inputPayload})
}
