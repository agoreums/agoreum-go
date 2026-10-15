package agoreum

// Funding an escrow-rail order: the SDK builds the same bytes the API encodes,
// refuses instructions whose encoding disagrees with their fields, and sends
// the two calls in order through the caller's own sender. The vector is the
// one apps/api/tests/test_funding_transactions.py holds to the contract's ABI.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	fundingEscrow   = "0x5e2f0b6f1c3a4d8e9b7a6c5d4e3f2a1b0c9d8e7f"
	fundingUSDC     = "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913"
	fundingApprove  = "0x095ea7b3" + "0000000000000000000000005e2f0b6f1c3a4d8e9b7a6c5d4e3f2a1b0c9d8e7f" + "0000000000000000000000000000000000000000000000000000000002720069"
	fundingCreate   = "0xae6d5d16" + "0000000000000000000000000000000066666666666666666666666666666666" + "0000000000000000000000000bd63110306b2f5552d0f9943d5f6c97f4363840" + "000000000000000000000000833589fcd6edb6e08f4c7c32d4f71b54bda02913" + "0000000000000000000000000000000000000000000000000000000002720069" + "0000000000000000000000000000000000000000000000000000000000093a80" + "0000000000000000000000000000000000000000000000000000000000127500"
	fundingEscrowID = "0x0000000000000000000000000000000066666666666666666666666666666666"
)

func fundingInstructionsJSON(t *testing.T, over map[string]any) []byte {
	t.Helper()
	body := map[string]any{
		"settlement_rail": "escrow", "chain_id": 8453, "escrow_contract": fundingEscrow,
		"token_address": fundingUSDC, "escrow_id": fundingEscrowID,
		"provider_address":  "0x0BD63110306b2f5552d0F9943d5f6C97f4363840",
		"amount_base_units": "41025641", "delivery_window_seconds": 604800,
		"auto_release_window_seconds": 1209600, "create_escrow_selector": "0xae6d5d16",
		"transactions": []map[string]any{
			{"step": "approve", "to": fundingUSDC, "data": fundingApprove, "value": "0"},
			{"step": "create_escrow", "to": fundingEscrow, "data": fundingCreate, "value": "0"},
		},
	}
	for k, v := range over {
		if v == nil {
			delete(body, k)
		} else {
			body[k] = v
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTheEncodersProduceTheVector(t *testing.T) {
	approve, err := EncodeApprove(fundingEscrow, "41025641")
	if err != nil || approve != fundingApprove {
		t.Fatalf("approve %q %v", approve, err)
	}
	create, err := EncodeCreateEscrow(fundingEscrowID, "0x0bd63110306b2f5552d0f9943d5f6c97f4363840", fundingUSDC, "41025641", "604800", "1209600")
	if err != nil || create != fundingCreate {
		t.Fatalf("createEscrow %q %v", create, err)
	}
	if other, _ := EncodeApprove(fundingEscrow, "1"); other == fundingApprove {
		t.Fatal("a different amount produced the same approval")
	}
}

func TestTheBuiltCallsAgreeWithTheAPIAndCarryTheChain(t *testing.T) {
	for _, over := range []map[string]any{nil, {"transactions": nil}} {
		built, err := TransactionsFromInstructions(fundingInstructionsJSON(t, over))
		if err != nil {
			t.Fatal(err)
		}
		if len(built) != 2 || built[0].Step != "approve" || built[0].To != fundingUSDC || built[0].Data != fundingApprove ||
			built[1].Step != "create_escrow" || built[1].To != fundingEscrow || built[1].Data != fundingCreate ||
			built[0].ChainID != 8453 || built[1].Value != "0" {
			t.Fatalf("built: %+v", built)
		}
	}
}

func TestInstructionsThatDisagreeWithThemselvesAreRefused(t *testing.T) {
	cases := map[string]struct {
		over   map[string]any
		reason string
	}{
		"a tampered approval": {map[string]any{"transactions": []map[string]any{
			{"step": "approve", "to": fundingUSDC, "data": strings.Replace(fundingApprove, "5e2f0b6f", "deadbeef", 1), "value": "0"},
			{"step": "create_escrow", "to": fundingEscrow, "data": fundingCreate, "value": "0"},
		}}, "approve transaction does not match"},
		"another escrow contract": {map[string]any{"transactions": []map[string]any{
			{"step": "approve", "to": fundingUSDC, "data": fundingApprove, "value": "0"},
			{"step": "create_escrow", "to": "0x" + strings.Repeat("11", 20), "data": fundingCreate, "value": "0"},
		}}, "create_escrow transaction does not match"},
		"ether attached": {map[string]any{"transactions": []map[string]any{
			{"step": "approve", "to": fundingUSDC, "data": fundingApprove, "value": "1000"},
			{"step": "create_escrow", "to": fundingEscrow, "data": fundingCreate, "value": "0"},
		}}, "approve transaction does not match"},
		"a summary that disagrees": {map[string]any{"amount_base_units": "1"}, "approve transaction does not match"},
		"another function":         {map[string]any{"create_escrow_selector": "0x12345678"}, "not createEscrow"},
		"a malformed provider":     {map[string]any{"provider_address": "not an address"}, "provider_address is not an address"},
		"a window past uint64":     {map[string]any{"delivery_window_seconds": json.Number("18446744073709551616")}, "does not fit uint64"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := TransactionsFromInstructions(fundingInstructionsJSON(t, tc.over))
			var bad *FundingInstructionsInconsistent
			if !errors.As(err, &bad) || !strings.Contains(bad.Reason, tc.reason) {
				t.Fatalf("want a refusal mentioning %q, got %v", tc.reason, err)
			}
		})
	}
}

func TestFundSendsBothCallsInOrderAndNothingWhenTheyDisagree(t *testing.T) {
	serve := func(body []byte) *Client {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		c, err := NewClient("ak_test", WithBaseURL(srv.URL))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	var steps []string
	send := func(_ context.Context, tx FundingTransaction) (string, error) {
		steps = append(steps, tx.Step)
		return "0x" + strings.Repeat(string(rune('0'+len(steps))), 64), nil
	}
	sent, err := serve(fundingInstructionsJSON(t, nil)).Orders.Fund(context.Background(), "order", send)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps, ",") != "approve,create_escrow" || len(sent) != 2 || sent[1].TransactionHash != "0x"+strings.Repeat("2", 64) {
		t.Fatalf("steps %v sent %+v", steps, sent)
	}

	steps = nil
	_, err = serve(fundingInstructionsJSON(t, map[string]any{"amount_base_units": "1"})).Orders.Fund(context.Background(), "order", send)
	var bad *FundingInstructionsInconsistent
	if !errors.As(err, &bad) || len(steps) != 0 {
		t.Fatalf("err %v, sent %v", err, steps)
	}
}
