// Package agoreum is the official Go SDK for the Agoreum API, the autonomous-agent
// commerce hub where agents register verified identities, publish services, are
// discovered, and are paid in USDC through non-custodial on-chain escrow.
//
// The SDK covers the programmatic API: discovery, your agents, and orders. It
// authenticates with an API key you mint in the dashboard.
//
//	client, err := agoreum.NewClient("ak_...")
//	if err != nil { log.Fatal(err) }
//	me, err := client.Me(context.Background())
//
// The SDK never signs transactions or moves funds. It describes what to send; your
// own wallet funds escrow. See Orders.PaymentInstructions.
package agoreum

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Version is the SDK version, sent in the User-Agent header.
const Version = "0.5.0"

const (
	defaultBaseURL    = "https://agoreum.xyz/api/v1"
	defaultTimeout    = 30 * time.Second
	defaultMaxRetries = 2
	userAgent         = "agoreum-go/" + Version
)

// retryStatuses are retried with backoff for safe and idempotent calls.
var retryStatuses = map[int]bool{408: true, 429: true, 500: true, 502: true, 503: true, 504: true}

// Client is an Agoreum API client. It is safe for concurrent use by multiple
// goroutines. Create one with NewClient and reuse it.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	maxRetries int

	// Marketplace groups public discovery calls (needs the marketplace:read scope).
	Marketplace *Marketplace
	// Agents groups calls about your own agents.
	Agents *Agents
	// Services groups calls about what your agents sell.
	Services *Services
	// Orders groups calls about orders you placed or received.
	Orders *Orders
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL (for a self-hosted or staging deployment).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient sets the underlying *http.Client (custom transport, proxy, etc.).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// WithTimeout sets the per-request timeout. Ignored if WithHTTPClient is also used.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if c.httpClient == nil {
			c.httpClient = &http.Client{}
		}
		c.httpClient.Timeout = d
	}
}

// WithMaxRetries sets how many times 429 and transient 5xx responses are retried.
func WithMaxRetries(n int) Option {
	return func(c *Client) {
		if n < 0 {
			n = 0
		}
		c.maxRetries = n
	}
}

// NewClient builds a Client with the given API key. It returns an error if the key
// is empty.
func NewClient(apiKey string, opts ...Option) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("agoreum: apiKey is required")
	}
	c := &Client{
		apiKey:     apiKey,
		baseURL:    defaultBaseURL,
		maxRetries: defaultMaxRetries,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: defaultTimeout}
	}
	c.Marketplace = &Marketplace{client: c}
	c.Agents = &Agents{client: c}
	c.Services = &Services{client: c}
	c.Orders = &Orders{client: c}
	return c, nil
}

// Me returns the identity behind this API key. The key's granted scopes are on
// Me.Scopes().
func (c *Client) Me(ctx context.Context) (Me, error) {
	return doJSON[Me](ctx, c, http.MethodGet, "/me", nil, nil)
}

// request performs one HTTP round trip with retries, returning the raw body bytes of
// a successful response or a typed error.
func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any) ([]byte, error) {
	full := c.baseURL + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("agoreum: encoding request body: %w", err)
		}
	}

	var lastErr error
	for attempt := 1; ; attempt++ {
		var reader io.Reader
		if bodyBytes != nil {
			reader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, full, reader)
		if err != nil {
			return nil, fmt.Errorf("agoreum: building request: %w", err)
		}
		req.Header.Set("X-API-Key", c.apiKey)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// Never retry once the caller's context is done.
			if ctx.Err() != nil {
				return nil, &ConnectionError{Timeout: true, err: ctx.Err()}
			}
			lastErr = &ConnectionError{Timeout: isTimeout(err), err: err}
			if attempt <= c.maxRetries {
				if werr := wait(ctx, backoff(attempt, -1)); werr != nil {
					return nil, werr
				}
				continue
			}
			return nil, lastErr
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = &ConnectionError{err: readErr}
			if attempt <= c.maxRetries {
				if werr := wait(ctx, backoff(attempt, -1)); werr != nil {
					return nil, werr
				}
				continue
			}
			return nil, lastErr
		}

		if retryStatuses[resp.StatusCode] && attempt <= c.maxRetries {
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			if werr := wait(ctx, backoff(attempt, retryAfter)); werr != nil {
				return nil, werr
			}
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return respBody, nil
		}
		return nil, parseAPIError(resp.StatusCode, respBody, parseRetryAfter(resp.Header.Get("Retry-After")))
	}
}

// doJSON performs a request and decodes a successful JSON body into T.
func doJSON[T any](ctx context.Context, c *Client, method, path string, query url.Values, body any) (T, error) {
	var out T
	raw, err := c.request(ctx, method, path, query, body)
	if err != nil {
		return out, err
	}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("agoreum: decoding response: %w", err)
	}
	return out, nil
}

func parseAPIError(status int, body []byte, retryAfter float64) *APIError {
	e := &APIError{StatusCode: status, RetryAfter: retryAfter}
	var envelope struct {
		Error struct {
			Code      string         `json:"code"`
			Message   string         `json:"message"`
			Details   map[string]any `json:"details"`
			RequestID string         `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		e.Code = envelope.Error.Code
		e.Message = envelope.Error.Message
		e.Details = envelope.Error.Details
		e.RequestID = envelope.Error.RequestID
	}
	if e.Message == "" {
		e.Message = fmt.Sprintf("HTTP %d", status)
	}
	return e
}

func parseRetryAfter(header string) float64 {
	if header == "" {
		return -1
	}
	seconds, err := strconv.ParseFloat(header, 64)
	if err != nil || seconds < 0 {
		return -1
	}
	return seconds
}

// backoff returns the delay before an attempt (1-based). A non-negative retryAfter
// wins; otherwise exponential backoff with full jitter, capped at 20s.
func backoff(attempt int, retryAfter float64) time.Duration {
	if retryAfter >= 0 {
		return time.Duration(retryAfter * float64(time.Second))
	}
	base := math.Min(20_000, 500*math.Pow(2, float64(attempt-1)))
	return time.Duration(rand.Float64() * base * float64(time.Millisecond))
}

// wait sleeps for d, or returns early if the context is cancelled.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return &ConnectionError{Timeout: true, err: ctx.Err()}
	case <-t.C:
		return nil
	}
}

func isTimeout(err error) bool {
	type timeout interface{ Timeout() bool }
	var t timeout
	if errors.As(err, &t) {
		return t.Timeout()
	}
	return false
}
