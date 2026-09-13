package agoreum

// The x402 rail in the Go SDK, against the shared vector.
//
// sdks/x402-vectors/auth-capture.json was generated from the API's own bindings
// and verified by them. Every SDK must build exactly that payload from the
// instructions, and must refuse each inconsistent variant before signing.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type x402Vector struct {
	Payer           string          `json:"payer"`
	Signature       string          `json:"signature"`
	Instructions    json.RawMessage `json:"instructions"`
	ExpectedPayload json.RawMessage `json:"expected_payload"`
	PaymentInfoHash string          `json:"payment_info_hash"`
	Inconsistent    []struct {
		Why          string          `json:"why"`
		Instructions json.RawMessage `json:"instructions"`
	} `json:"inconsistent_instructions"`
}

func loadVector(t *testing.T) x402Vector {
	t.Helper()
	// The module's own copy. The canonical file lives two directories up in
	// the monorepo, which a standalone clone of the published module cannot
	// reach; check_go_vectors_synced.py asserts the two are byte-identical.
	raw, err := os.ReadFile(filepath.Join("testdata", "x402-auth-capture.json"))
	if err != nil {
		t.Fatalf("read vector: %v", err)
	}
	var v x402Vector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode vector: %v", err)
	}
	return v
}

func canonical(t *testing.T, raw []byte) any {
	t.Helper()
	var out any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestX402AuthorizationReadsAndSummarises(t *testing.T) {
	v := loadVector(t)
	auth, err := NewX402Authorization(v.Instructions)
	if err != nil {
		t.Fatalf("NewX402Authorization: %v", err)
	}
	if auth.Instructions.ChainID != 84532 || auth.Instructions.AmountBaseUnits != "102564102" {
		t.Errorf("fields = %+v", auth.Instructions)
	}
	if auth.Instructions.AuthorizationExpiry != auth.Instructions.AutoReleaseAt+auth.Instructions.ClaimLapseSeconds {
		t.Errorf("expiry arithmetic")
	}
	s := auth.Summary()
	for _, want := range []string{"102.564102 USDC", auth.Instructions.ProviderAddress, auth.Instructions.OperatorContract} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q: %s", want, s)
		}
	}
}

func TestX402SignBuildsExactlyTheExpectedPayload(t *testing.T) {
	v := loadVector(t)
	auth, err := NewX402Authorization(v.Instructions)
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Payload struct {
			Authorization map[string]string `json:"authorization"`
		} `json:"payload"`
	}
	_ = json.Unmarshal(v.ExpectedPayload, &expected)

	payload, err := auth.Sign(context.Background(), v.Payer, func(_ context.Context, doc EIP712TypedData) (string, error) {
		if doc.PrimaryType != "ReceiveWithAuthorization" {
			t.Errorf("primaryType = %q", doc.PrimaryType)
		}
		if !reflect.DeepEqual(doc.Message, expected.Payload.Authorization) {
			t.Errorf("the SDK must hand the wallet the exact message:\n%v\n%v", doc.Message, expected.Payload.Authorization)
		}
		return v.Signature, nil
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, _ := json.Marshal(payload)
	if !reflect.DeepEqual(canonical(t, got), canonical(t, v.ExpectedPayload)) {
		t.Errorf("payload differs from the vector:\n%s\n%s", got, v.ExpectedPayload)
	}
	// The instructions were not mutated by filling in the payer.
	if auth.Instructions.TypedData.Message["from"] != "0x0000000000000000000000000000000000000000" {
		t.Errorf("instructions mutated: %q", auth.Instructions.TypedData.Message["from"])
	}
}

func TestX402InconsistentInstructionsAreRefused(t *testing.T) {
	v := loadVector(t)
	if len(v.Inconsistent) < 5 {
		t.Fatalf("the vector carries %d inconsistent cases; expected several", len(v.Inconsistent))
	}
	for _, c := range v.Inconsistent {
		_, err := NewX402Authorization(c.Instructions)
		if !errors.Is(err, ErrX402InstructionsInconsistent) {
			t.Errorf("%s: err = %v", c.Why, err)
		}
	}
}

func TestX402DirectRailIsRefusedByName(t *testing.T) {
	v := loadVector(t)
	var m map[string]any
	_ = json.Unmarshal(v.Instructions, &m)
	m["settlement_rail"] = "escrow"
	raw, _ := json.Marshal(m)
	if _, err := NewX402Authorization(raw); !errors.Is(err, ErrNotX402Rail) {
		t.Errorf("err = %v", err)
	}
}

func TestX402JunkSignatureIsRefused(t *testing.T) {
	v := loadVector(t)
	auth, _ := NewX402Authorization(v.Instructions)
	if _, err := auth.Sign(context.Background(), v.Payer, func(context.Context, EIP712TypedData) (string, error) {
		return "not a signature", nil
	}); err == nil {
		t.Error("expected an error")
	}
}

func TestX402ClientFetchesSignsAndSubmits(t *testing.T) {
	v := loadVector(t)
	var submitted []byte
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/payment-instructions"):
			writeJSON(w, 200, string(v.Instructions))
		case strings.HasSuffix(r.URL.Path, "/payment"):
			submitted, _ = io.ReadAll(r.Body)
			writeJSON(w, 200, `{"order_id":"o","order_reference":"X402-VEC","status":"collected","payment_info_hash":"`+v.PaymentInfoHash+`","transaction_hash":"0xabc","block_number":123,"chain_id":84532,"network":"eip155:84532","payer":"`+v.Payer+`","amount_base_units":"102564102","relayer":"0xr","order_status":"pending_payment","explorer_url":"https://sepolia.basescan.org","note":"n"}`)
		default:
			writeJSON(w, 404, `{"error":{"code":"not_found","message":"no"}}`)
		}
	})
	ctx := context.Background()
	auth, err := c.Orders.X402Authorization(ctx, "0f9a3c2e-4b1d-4e7a-9c0b-1a2b3c4d5e6f")
	if err != nil {
		t.Fatalf("X402Authorization: %v", err)
	}
	payload, err := auth.Sign(ctx, v.Payer, func(context.Context, EIP712TypedData) (string, error) { return v.Signature, nil })
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Orders.SubmitX402Payment(ctx, "0f9a3c2e-4b1d-4e7a-9c0b-1a2b3c4d5e6f", payload)
	if err != nil {
		t.Fatalf("SubmitX402Payment: %v", err)
	}
	if !reflect.DeepEqual(canonical(t, submitted), canonical(t, v.ExpectedPayload)) {
		t.Errorf("submitted payload differs from the vector:\n%s", submitted)
	}
	if result.Status != "collected" || !result.Relayed() || result.OrderStatus != "pending_payment" {
		t.Errorf("result = %+v", result)
	}
	if !strings.Contains(result.StillToVerify(), "order becomes funded") {
		t.Errorf("still to verify: %s", result.StillToVerify())
	}
}

func TestX402OneCallForm(t *testing.T) {
	v := loadVector(t)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/payment-instructions") {
			writeJSON(w, 200, string(v.Instructions))
			return
		}
		writeJSON(w, 200, `{"order_id":"o","order_reference":"X402-VEC","status":"submitted","payment_info_hash":"0x00","transaction_hash":"0xabc","block_number":null,"chain_id":84532,"network":"eip155:84532","payer":"`+v.Payer+`","amount_base_units":"102564102","relayer":"0xr","order_status":"pending_payment","explorer_url":"","note":"n"}`)
	})
	result, err := c.Orders.AuthorizeAndSubmitX402(context.Background(), "o", v.Payer, func(context.Context, EIP712TypedData) (string, error) { return v.Signature, nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "submitted" || !strings.Contains(result.StillToVerify(), "not yet mined") {
		t.Errorf("result = %+v", result)
	}
}
