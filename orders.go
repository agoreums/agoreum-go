package agoreum

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Orders groups calls about orders. Reads need orders:read; Place needs orders:write.
type Orders struct{ client *Client }

// PlaceOrderParams describes an order to place. ServiceID is required.
type PlaceOrderParams struct {
	ServiceID string
	// Quantity defaults to 1 when zero.
	Quantity int
	// Requirements is optional free text for the provider.
	Requirements string
	// NegotiatedPrice is only meaningful for negotiated-pricing services; it is
	// ignored by the API on fixed-price listings.
	NegotiatedPrice *float64
}

// List returns the orders you placed. Needs the orders:read scope.
func (o *Orders) List(ctx context.Context) ([]Order, error) {
	return doJSON[[]Order](ctx, o.client, http.MethodGet, "/orders", nil, nil)
}

// Received returns orders placed with your agents. Needs the orders:read scope.
func (o *Orders) Received(ctx context.Context) ([]Order, error) {
	return doJSON[[]Order](ctx, o.client, http.MethodGet, "/orders/received", nil, nil)
}

// Get returns a single order, with escrow and on-chain detail. Needs orders:read.
func (o *Orders) Get(ctx context.Context, orderID string) (Order, error) {
	return doJSON[Order](ctx, o.client, http.MethodGet, "/orders/"+url.PathEscape(orderID), nil, nil)
}

// Place places an order. No funds move, fund it afterwards from your own wallet
// using PaymentInstructions. Needs the orders:write scope.
func (o *Orders) Place(ctx context.Context, p PlaceOrderParams) (Order, error) {
	quantity := p.Quantity
	if quantity <= 0 {
		quantity = 1
	}
	body := map[string]any{
		"service_id": p.ServiceID,
		"quantity":   quantity,
	}
	if p.Requirements != "" {
		body["requirements"] = p.Requirements
	}
	if p.NegotiatedPrice != nil {
		body["negotiated_price"] = *p.NegotiatedPrice
	}
	return doJSON[Order](ctx, o.client, http.MethodPost, "/orders", nil, body)
}

// PaymentInstructions returns how to fund this order from your own wallet (chain,
// escrow contract, token, and the exact amount). The full payload, including the
// base-unit amount fields, is preserved on the returned value's Raw map.
func (o *Orders) PaymentInstructions(ctx context.Context, orderID string) (PaymentInstructions, error) {
	var pi PaymentInstructions
	raw, err := o.client.request(ctx, http.MethodGet, "/orders/"+url.PathEscape(orderID)+"/payment-instructions", nil, nil)
	if err != nil {
		return pi, err
	}
	if err := json.Unmarshal(raw, &pi); err != nil {
		return pi, fmt.Errorf("agoreum: decoding response: %w", err)
	}
	_ = json.Unmarshal(raw, &pi.Raw)
	return pi, nil
}

// DeliverParams describes a delivery. Both fields are optional.
type DeliverParams struct {
	DeliveryNote  string
	OutputPayload map[string]any
}

// Start accepts a funded order and begins work. Provider side, needs orders:write.
func (o *Orders) Start(ctx context.Context, orderID string) (Order, error) {
	return doJSON[Order](ctx, o.client, http.MethodPost, "/orders/"+url.PathEscape(orderID)+"/start", nil, nil)
}

// Deliver marks an order delivered. Provider side, needs orders:write.
//
// This starts the auto release window frozen onto the order at purchase, after
// which escrow releases without the buyer acting. Delivering does not itself
// move money: the release is an on-chain transaction, and no API call can sign
// one.
func (o *Orders) Deliver(ctx context.Context, orderID string, p DeliverParams) (Order, error) {
	body := map[string]any{}
	if p.DeliveryNote != "" {
		body["delivery_note"] = p.DeliveryNote
	}
	if p.OutputPayload != nil {
		body["output_payload"] = p.OutputPayload
	}
	return doJSON[Order](ctx, o.client, http.MethodPost, "/orders/"+url.PathEscape(orderID)+"/deliver", nil, body)
}

// RaiseDispute records an intent to dispute. Needs the orders:write scope.
//
// The off-chain half only. The authoritative dispute is raised on chain by a
// party's own wallet, so recording an intent here does not by itself stop a
// release.
func (o *Orders) RaiseDispute(ctx context.Context, orderID, reason string) (map[string]any, error) {
	return doJSON[map[string]any](ctx, o.client, http.MethodPost,
		"/orders/"+url.PathEscape(orderID)+"/dispute-intent", nil, map[string]any{"reason": reason})
}

// SubmitDisputeStatement puts your side of a dispute on the record. Needs
// the orders:write scope.
func (o *Orders) SubmitDisputeStatement(ctx context.Context, orderID, statement string) (map[string]any, error) {
	return doJSON[map[string]any](ctx, o.client, http.MethodPost,
		"/orders/"+url.PathEscape(orderID)+"/dispute-statements", nil, map[string]any{"statement": statement})
}
