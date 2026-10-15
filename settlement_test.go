package agoreum

// Releasing, refunding and disputing from your own wallet: the SDK builds the
// same calldata as eth_abi does from the escrow's ABI (vectors held to the ABI
// in apps/api/tests/test_funding_transactions.py), refuses options that
// disagree with themselves or actions that are not open yet, and sends through
// the caller's own sender.

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
	settleOrderID      = "order-1"
	settleEscrow       = "0x5e2f0b6f1c3a4d8e9b7a6c5d4e3f2a1b0c9d8e7f"
	settleEscrowID     = "0x0000000000000000000000000000000066666666666666666666666666666666"
	settleRelease      = "0x67d42a8b0000000000000000000000000000000066666666666666666666666666666666"
	settleRefund       = "0x7249fbb60000000000000000000000000000000066666666666666666666666666666666"
	settleDisputeASCII = "0x84146b9d0000000000000000000000000000000066666666666666666666666666666666" +
		"0000000000000000000000000000000000000000000000000000000000000040" +
		"0000000000000000000000000000000000000000000000000000000000000016" +
		"4e6f7468696e67207761732064656c6976657265642e00000000000000000000"
	settleDisputeUnicode = "0x84146b9d0000000000000000000000000000000066666666666666666666666666666666" +
		"0000000000000000000000000000000000000000000000000000000000000040" +
		"0000000000000000000000000000000000000000000000000000000000000054" +
		"44c3a96c61692064c3a970617373c3a92c207269656e206e276120c3a974c3a9" +
		"206c697672c3a920e28692207878787878787878787878787878787878787878" +
		"7878787878787878787878787878787878787878000000000000000000000000"
)

// Multi-byte UTF-8 (e acute two bytes, the arrow three): length and padding count bytes.
var settleUnicodeReason = "D" + string(rune(0xe9)) + "lai d" + string(rune(0xe9)) + "pass" + string(rune(0xe9)) +
	", rien n'a " + string(rune(0xe9)) + "t" + string(rune(0xe9)) + " livr" + string(rune(0xe9)) + " " +
	string(rune(0x2192)) + " " + strings.Repeat("x", 40)

func settleOptions(t *testing.T, mutate func(map[string]any)) SettlementOptions {
	t.Helper()
	act := func(name, selector string, calldata any) map[string]any {
		args := []map[string]any{{"name": "escrowId", "type": "bytes32", "value": settleEscrowID}}
		if name == "dispute" {
			args = append(args, map[string]any{"name": "reason", "type": "string", "value": nil})
		}
		return map[string]any{"action": name, "available": true, "who": "x", "reason": nil, "available_at": nil,
			"function": name, "selector": selector, "arguments": args, "calldata": calldata, "contract": nil}
	}
	body := map[string]any{
		"order_id": "o", "order_reference": "AGO-1", "settlement_rail": "escrow", "chain_id": 8453,
		"network_name": "Base", "escrow_contract": settleEscrow, "escrow_id": settleEscrowID,
		"onchain_status": "funded", "contract_paused": false, "your_roles": []string{"buyer"},
		"actions": []map[string]any{
			act("release", "0x67d42a8b", settleRelease), act("refund", "0x7249fbb6", settleRefund), act("dispute", "0x84146b9d", nil),
		},
		"explorer_url": "https://basescan.org", "note": "",
	}
	if mutate != nil {
		mutate(body)
	}
	raw, _ := json.Marshal(body)
	var out SettlementOptions
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTheSettlementEncodersProduceTheABIVectors(t *testing.T) {
	for _, c := range []struct {
		got  func() (string, error)
		want string
	}{
		{func() (string, error) { return EncodeRelease(settleEscrowID) }, settleRelease},
		{func() (string, error) { return EncodeRefund(settleEscrowID) }, settleRefund},
		{func() (string, error) { return EncodeDispute(settleEscrowID, "Nothing was delivered.") }, settleDisputeASCII},
		{func() (string, error) { return EncodeDispute(settleEscrowID, settleUnicodeReason) }, settleDisputeUnicode},
	} {
		if got, err := c.got(); err != nil || got != c.want {
			t.Errorf("got %q %v, want %q", got, err, c.want)
		}
	}
	if other, _ := EncodeRelease("0x" + strings.Repeat("11", 32)); other == settleRelease {
		t.Error("another escrow produced the same release")
	}
}

func TestEachActionBecomesOneCallToTheEscrow(t *testing.T) {
	for action, want := range map[string]string{"release": settleRelease, "refund": settleRefund, "dispute": settleDisputeASCII} {
		tx, err := TransactionFromOptions(settleOptions(t, nil), action, "Nothing was delivered.")
		if err != nil || tx.To != settleEscrow || tx.Data != want || tx.ChainID != 8453 || tx.Value != "0" {
			t.Errorf("%s: %+v %v", action, tx, err)
		}
	}
}

func TestAnActionThatIsNotOpenCarriesTheAPIsReason(t *testing.T) {
	opts := settleOptions(t, func(b map[string]any) {
		a := b["actions"].([]map[string]any)[1]
		a["available"], a["reason"], a["available_at"] = false, "Not yet.", "2026-10-20T00:00:00Z"
	})
	_, err := TransactionFromOptions(opts, "refund", "")
	var closed *SettlementNotAvailable
	if !errors.As(err, &closed) || closed.Reason != "Not yet." || closed.AvailableAt != "2026-10-20T00:00:00Z" {
		t.Fatalf("got %v", err)
	}
}

func TestSettlementOptionsThatDisagreeWithThemselvesAreRefused(t *testing.T) {
	cases := map[string]struct {
		mutate func(map[string]any)
		action string
		reason string
	}{
		"tampered calldata": {func(b map[string]any) {
			b["actions"].([]map[string]any)[0]["calldata"] = strings.Replace(settleRelease, "6666", "7777", 1)
		}, "release", "calldata does not match"},
		"another function": {func(b map[string]any) { b["actions"].([]map[string]any)[0]["selector"] = "0x12345678" }, "release", "selector is not the escrow's own"},
		"another contract": {func(b map[string]any) {
			b["actions"].([]map[string]any)[1]["contract"] = "0x" + strings.Repeat("11", 20)
		}, "refund", "another contract"},
		"another escrow": {func(b map[string]any) {
			b["actions"].([]map[string]any)[0]["arguments"] = []map[string]any{{"name": "escrowId", "type": "bytes32", "value": "0x" + strings.Repeat("11", 32)}}
		}, "release", "names another escrow"},
		"a missing action":      {func(b map[string]any) { b["actions"] = b["actions"].([]map[string]any)[:1] }, "refund", "do not list refund"},
		"a bad escrow contract": {func(b map[string]any) { b["escrow_contract"] = "not an address" }, "release", "escrow_contract is not an address"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := TransactionFromOptions(settleOptions(t, tc.mutate), tc.action, "")
			var bad *SettlementOptionsInconsistent
			if !errors.As(err, &bad) || !strings.Contains(bad.Reason, tc.reason) {
				t.Fatalf("want a refusal mentioning %q, got %v", tc.reason, err)
			}
		})
	}
}

func TestADisputeNeedsAShortReasonAndOtherRailsAreRefused(t *testing.T) {
	if _, err := TransactionFromOptions(settleOptions(t, nil), "dispute", "  "); err == nil || !strings.Contains(err.Error(), "needs a reason") {
		t.Errorf("blank reason: %v", err)
	}
	if _, err := TransactionFromOptions(settleOptions(t, nil), "dispute", strings.Repeat(string(rune(0xe9)), 501)); err == nil || !strings.Contains(err.Error(), "under 1000 bytes") {
		t.Errorf("long reason: %v", err)
	}
	x402 := settleOptions(t, func(b map[string]any) { b["settlement_rail"] = "x402-auth-capture" })
	if _, err := TransactionFromOptions(x402, "release", ""); err == nil || !strings.Contains(err.Error(), "x402-auth-capture rail") {
		t.Errorf("x402: %v", err)
	}
}

func TestSettleSendsOneCallAndReturnsTheHash(t *testing.T) {
	body, _ := json.Marshal(settleOptions(t, nil))
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient("ak_test", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	sent, err := c.Orders.Settle(context.Background(), settleOrderID, "release", "", func(_ context.Context, tx SettlementTransaction) (string, error) {
		seen = append(seen, tx.Data)
		return "0x" + strings.Repeat("b", 64), nil
	})
	if err != nil || path != "/orders/"+settleOrderID+"/settlement-options" || len(seen) != 1 || seen[0] != settleRelease ||
		sent.TransactionHash != "0x"+strings.Repeat("b", 64) || sent.Step != "release" {
		t.Fatalf("err %v path %s seen %v sent %+v", err, path, seen, sent)
	}
}
