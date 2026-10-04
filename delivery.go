package agoreum

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// DeliverySignatureRequest is what the provider's wallet signs to record
// delivery on chain (API 0.20.0). DeliveredAt is when the chain recorded
// delivery, nil until it has.
type DeliverySignatureRequest struct {
	OrderID          string          `json:"order_id"`
	SettlementRail   string          `json:"settlement_rail"`
	ChainID          int64           `json:"chain_id"`
	Contract         string          `json:"contract"`
	Signer           string          `json:"signer"`
	Digest           string          `json:"digest"`
	TypedData        EIP712TypedData `json:"typed_data"`
	DeliveryDeadline *string         `json:"delivery_deadline"`
	DeliveredAt      *string         `json:"delivered_at"`
	Note             string          `json:"note"`
}

// DeliveryRecordResult is what the relay did: Status is "recorded",
// "submitted" (sent, not yet mined) or "already_recorded".
type DeliveryRecordResult struct {
	OrderID         string  `json:"order_id"`
	Status          string  `json:"status"`
	TransactionHash *string `json:"transaction_hash"`
	ExplorerURL     *string `json:"explorer_url"`
	Note            string  `json:"note"`
}

// DeliverySignature returns what the provider's wallet signs to record
// delivery on chain. A contract that predates on-chain delivery answers a
// conflict with code delivery_recording_not_supported, and Deliver is all such
// an order needs.
func (o *Orders) DeliverySignature(ctx context.Context, orderID string) (DeliverySignatureRequest, error) {
	return doJSON[DeliverySignatureRequest](ctx, o.client, http.MethodGet,
		"/orders/"+url.PathEscape(orderID)+"/delivery-signature", nil, nil)
}

// RecordDelivery relays the provider's signed statement of delivery to the
// contract. Agoreum checks it against the provider, pays the gas and moves no
// money. Safe to call again.
func (o *Orders) RecordDelivery(ctx context.Context, orderID, signature string) (DeliveryRecordResult, error) {
	return doJSON[DeliveryRecordResult](ctx, o.client, http.MethodPost,
		"/orders/"+url.PathEscape(orderID)+"/delivery-signature", nil, map[string]any{"signature": signature})
}

// SignAndRecordDelivery fetches the delivery statement, signs it with the
// caller's own signer and relays it. The SDK never sees a key. Nothing is
// signed when the chain already recorded delivery. It must happen before the
// delivery deadline, after which the contract refuses it.
func (o *Orders) SignAndRecordDelivery(ctx context.Context, orderID string, sign SignTypedData) (DeliveryRecordResult, error) {
	req, err := o.DeliverySignature(ctx, orderID)
	if err != nil {
		return DeliveryRecordResult{}, err
	}
	if req.DeliveredAt != nil && *req.DeliveredAt != "" {
		return DeliveryRecordResult{
			OrderID: orderID,
			Status:  "already_recorded",
			Note:    "Delivery was already recorded on chain for this order.",
		}, nil
	}
	signature, err := sign(ctx, req.TypedData)
	if err != nil {
		return DeliveryRecordResult{}, err
	}
	if !strings.HasPrefix(signature, "0x") || len(signature) < 132 {
		return DeliveryRecordResult{}, errors.New("agoreum: sign must return a 0x-prefixed hex signature of 65 bytes or more")
	}
	return o.RecordDelivery(ctx, orderID, signature)
}
