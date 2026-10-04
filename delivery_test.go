package agoreum

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Recording delivery on chain (API 0.20.0, internal audit F-3). Each case is
// paired with its opposite: the signature is relayed when delivery is not yet
// recorded, and nothing is signed when it is.

const deliveryOrder = "33333333-3333-3333-3333-333333333333"

var deliverySignature = "0x" + strings.Repeat("ab", 65)

func deliveryRequest(deliveredAt string) string {
	at := "null"
	if deliveredAt != "" {
		at = fmt.Sprintf("%q", deliveredAt)
	}
	return fmt.Sprintf(`{"order_id": %q, "settlement_rail": "escrow", "chain_id": 8453,
  "contract": "0x1111111111111111111111111111111111111111", "signer": "0x2222222222222222222222222222222222222222",
  "digest": "0x%s",
  "typed_data": {"types": {"Delivery": [{"name": "escrowId", "type": "bytes32"}]}, "primaryType": "Delivery",
    "domain": {"name": "AgoreumEscrow", "version": "1", "chainId": 8453, "verifyingContract": "0x1111111111111111111111111111111111111111"},
    "message": {"escrowId": "0x%s"}},
  "delivery_deadline": "2027-01-01T00:00:00Z", "delivered_at": %s, "note": "Sign typed_data."}`,
		deliveryOrder, strings.Repeat("cd", 32), strings.Repeat("77", 32), at)
}

func deliveryServer(t *testing.T, deliveredAt string, posted *[]string) *Client {
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders/"+deliveryOrder+"/delivery-signature" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Method == http.MethodGet {
			writeJSON(w, 200, deliveryRequest(deliveredAt))
			return
		}
		body, _ := io.ReadAll(r.Body)
		*posted = append(*posted, string(body))
		writeJSON(w, 200, fmt.Sprintf(`{"order_id": %q, "status": "recorded", "transaction_hash": "0x%s",
  "explorer_url": null, "note": "Delivery is recorded on chain."}`, deliveryOrder, strings.Repeat("ee", 32)))
	})
}

func TestSignAndRecordDeliveryRelaysTheSignature(t *testing.T) {
	var posted []string
	c := deliveryServer(t, "", &posted)
	var signed []EIP712TypedData
	result, err := c.Orders.SignAndRecordDelivery(context.Background(), deliveryOrder,
		func(_ context.Context, doc EIP712TypedData) (string, error) {
			signed = append(signed, doc)
			return deliverySignature, nil
		})
	if err != nil {
		t.Fatalf("SignAndRecordDelivery: %v", err)
	}
	if len(signed) != 1 || signed[0].PrimaryType != "Delivery" || signed[0].Message["escrowId"] != "0x"+strings.Repeat("77", 32) {
		t.Fatalf("signed %+v, want the API's Delivery document", signed)
	}
	if len(posted) != 1 {
		t.Fatalf("posted %d times, want 1", len(posted))
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(posted[0]), &body); err != nil || body["signature"] != deliverySignature {
		t.Fatalf("posted %q, want the signature", posted[0])
	}
	if result.Status != "recorded" {
		t.Fatalf("status %q, want recorded", result.Status)
	}
}

func TestSignAndRecordDeliverySignsNothingWhenAlreadyRecorded(t *testing.T) {
	var posted []string
	c := deliveryServer(t, "2026-12-30T00:00:00Z", &posted)
	result, err := c.Orders.SignAndRecordDelivery(context.Background(), deliveryOrder,
		func(context.Context, EIP712TypedData) (string, error) {
			t.Fatal("signed although delivery was already recorded")
			return "", nil
		})
	if err != nil {
		t.Fatalf("SignAndRecordDelivery: %v", err)
	}
	if result.Status != "already_recorded" || len(posted) != 0 {
		t.Fatalf("status %q, posted %d; want already_recorded and nothing posted", result.Status, len(posted))
	}
}

func TestSignAndRecordDeliveryRefusesAMalformedSignature(t *testing.T) {
	var posted []string
	c := deliveryServer(t, "", &posted)
	_, err := c.Orders.SignAndRecordDelivery(context.Background(), deliveryOrder,
		func(context.Context, EIP712TypedData) (string, error) { return strings.Repeat("ab", 65), nil })
	if err == nil || len(posted) != 0 {
		t.Fatalf("err %v, posted %d; want a refusal and nothing posted", err, len(posted))
	}
}
