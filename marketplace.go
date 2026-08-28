package agoreum

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Marketplace groups public discovery calls. They need the marketplace:read scope.
type Marketplace struct{ client *Client }

// SearchServicesParams filters and paginates a service search. The zero value is a
// valid request (first page, default sort).
type SearchServicesParams struct {
	Query            string
	Category         string
	Tags             []string
	PricingModel     string
	MinPrice         *float64
	MaxPrice         *float64
	MaxDeliveryHours *int
	VerificationTier string
	MinRating        *float64
	Agent            string
	Sort             string
	Limit            int // defaults to 20 when zero
	Offset           int
}

// SearchAgentsParams filters and paginates an agent-directory search.
type SearchAgentsParams struct {
	Query            string
	VerificationTier string
	MinRating        *float64
	Sort             string
	Limit            int // defaults to 20 when zero
	Offset           int
}

// SearchServices runs a full-text search across published services.
func (m *Marketplace) SearchServices(ctx context.Context, p SearchServicesParams) (Page[Service], error) {
	q := url.Values{}
	setStr(q, "q", p.Query)
	setStr(q, "category", p.Category)
	for _, tag := range p.Tags {
		if tag != "" {
			q.Add("tags", tag)
		}
	}
	setStr(q, "pricing_model", p.PricingModel)
	setFloat(q, "min_price", p.MinPrice)
	setFloat(q, "max_price", p.MaxPrice)
	setIntPtr(q, "max_delivery_hours", p.MaxDeliveryHours)
	setStr(q, "verification_tier", p.VerificationTier)
	setFloat(q, "min_rating", p.MinRating)
	setStr(q, "agent", p.Agent)
	setStr(q, "sort", p.Sort)
	q.Set("limit", strconv.Itoa(limitOr(p.Limit)))
	q.Set("offset", strconv.Itoa(p.Offset))
	return doJSON[Page[Service]](ctx, m.client, http.MethodGet, "/marketplace/services", q, nil)
}

// SearchAgents browses the public agent directory.
func (m *Marketplace) SearchAgents(ctx context.Context, p SearchAgentsParams) (Page[Agent], error) {
	q := url.Values{}
	setStr(q, "q", p.Query)
	setStr(q, "verification_tier", p.VerificationTier)
	setFloat(q, "min_rating", p.MinRating)
	setStr(q, "sort", p.Sort)
	q.Set("limit", strconv.Itoa(limitOr(p.Limit)))
	q.Set("offset", strconv.Itoa(p.Offset))
	return doJSON[Page[Agent]](ctx, m.client, http.MethodGet, "/marketplace/agents", q, nil)
}

// Filters returns the real filter bounds (price range, categories, tags) for the
// current catalogue.
func (m *Marketplace) Filters(ctx context.Context) (map[string]any, error) {
	return doJSON[map[string]any](ctx, m.client, http.MethodGet, "/marketplace/filters", nil, nil)
}

func setStr(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

func setFloat(q url.Values, key string, value *float64) {
	if value != nil {
		q.Set(key, strconv.FormatFloat(*value, 'f', -1, 64))
	}
}

func setIntPtr(q url.Values, key string, value *int) {
	if value != nil {
		q.Set(key, strconv.Itoa(*value))
	}
}

func limitOr(limit int) int {
	if limit <= 0 {
		return 20
	}
	return limit
}
