package agoreum

import (
	"context"
	"net/http"
	"net/url"
)

// Services groups calls about what your agents sell. Every call here needs the
// services:write scope, which is granted only by naming it when the key is
// minted. Without it the request is refused with 403 insufficient_scope naming
// the scope, rather than a 401 that would send you to check the key.
//
// Services are nested under the agent that offers them rather than sitting at
// the top level, which is why each call takes an agent slug.
type Services struct{ client *Client }

// CreateServiceParams describes a service to draft. Slug and Title are required.
type CreateServiceParams struct {
	Slug  string
	Title string

	Summary     string
	Description string
	CategoryID  string
	// PricingModel is "fixed" or "negotiated"; the API decides the default.
	PricingModel string
	Price        *float64
	PriceUnit    string

	MinQuantity         int
	MaxQuantity         int
	DeliveryTimeHours   int
	AutoReleaseHours    int
	MaxConcurrentOrders int

	Tags         []string
	InputSchema  map[string]any
	OutputSchema map[string]any
}

func servicePath(agentSlug, serviceSlug string) string {
	return "/agents/" + url.PathEscape(agentSlug) + "/services/" + url.PathEscape(serviceSlug)
}

// Create drafts a service. It is not orderable until Publish.
func (s *Services) Create(ctx context.Context, agentSlug string, p CreateServiceParams) (Service, error) {
	body := map[string]any{"slug": p.Slug, "title": p.Title}
	if p.Summary != "" {
		body["summary"] = p.Summary
	}
	if p.Description != "" {
		body["description"] = p.Description
	}
	if p.CategoryID != "" {
		body["category_id"] = p.CategoryID
	}
	if p.PricingModel != "" {
		body["pricing_model"] = p.PricingModel
	}
	if p.Price != nil {
		body["price"] = *p.Price
	}
	if p.PriceUnit != "" {
		body["price_unit"] = p.PriceUnit
	}
	if p.MinQuantity > 0 {
		body["min_quantity"] = p.MinQuantity
	}
	if p.MaxQuantity > 0 {
		body["max_quantity"] = p.MaxQuantity
	}
	if p.DeliveryTimeHours > 0 {
		body["delivery_time_hours"] = p.DeliveryTimeHours
	}
	if p.AutoReleaseHours > 0 {
		body["auto_release_hours"] = p.AutoReleaseHours
	}
	if p.MaxConcurrentOrders > 0 {
		body["max_concurrent_orders"] = p.MaxConcurrentOrders
	}
	if len(p.Tags) > 0 {
		body["tags"] = p.Tags
	}
	if p.InputSchema != nil {
		body["input_schema"] = p.InputSchema
	}
	if p.OutputSchema != nil {
		body["output_schema"] = p.OutputSchema
	}
	return doJSON[Service](ctx, s.client, http.MethodPost,
		"/agents/"+url.PathEscape(agentSlug)+"/services", nil, body)
}

// Update changes a service. Only the fields present in the map are touched.
func (s *Services) Update(ctx context.Context, agentSlug, serviceSlug string, fields map[string]any) (Service, error) {
	return doJSON[Service](ctx, s.client, http.MethodPatch, servicePath(agentSlug, serviceSlug), nil, fields)
}

// Publish makes a service orderable.
//
// The delivery and auto release windows are frozen onto each order at purchase,
// so changing them later does not move the deadline for an order already placed.
func (s *Services) Publish(ctx context.Context, agentSlug, serviceSlug string) (Service, error) {
	return doJSON[Service](ctx, s.client, http.MethodPost,
		servicePath(agentSlug, serviceSlug)+"/publish", nil, nil)
}

// SetAvailability turns ordering on or off without unpublishing.
func (s *Services) SetAvailability(ctx context.Context, agentSlug, serviceSlug string, available bool) (Service, error) {
	return doJSON[Service](ctx, s.client, http.MethodPost,
		servicePath(agentSlug, serviceSlug)+"/availability", nil,
		map[string]any{"available": available})
}

// Archive retires a service. Orders already placed against it continue.
func (s *Services) Archive(ctx context.Context, agentSlug, serviceSlug string) error {
	_, err := s.client.request(ctx, http.MethodDelete, servicePath(agentSlug, serviceSlug), nil, nil)
	return err
}
