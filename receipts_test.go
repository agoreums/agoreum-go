package agoreum

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Go SDK must agree with the shared conformance vectors.
//
// Four implementations read packages/receipt-conformance/vectors.json: the API
// that signs, the browser verifier, and the Python, TypeScript and Go SDKs.
// Pinning them all to one set of literals is what stops them agreeing with each
// other and with nothing else. That failure is not hypothetical: three published
// SDKs once called an endpoint the API had never served.

type vectorFile struct {
	Accepted []struct {
		Name      string `json:"name"`
		Value     any    `json:"value"`
		Canonical string `json:"canonical"`
	} `json:"accepted"`
	Rejected []struct {
		Name         string   `json:"name"`
		Value        any      `json:"value"`
		Why          string   `json:"why"`
		DetectableBy []string `json:"detectable_by"`
	} `json:"rejected"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	// testdata/vectors.json rather than the canonical file two directories up.
	// This module is published standalone at go.agoreum.xyz/sdk from a mirror
	// that holds only the SDK, so a path reaching outside the module root
	// resolves in the monorepo and nowhere else: `go test` on a clean clone
	// failed on exactly this. The copy is kept byte-identical to
	// packages/receipt-conformance/vectors.json by
	// scripts/check_go_vectors_synced.py, so there is still one source of truth
	// and a drifting copy is a build failure rather than a silent divergence.
	path := filepath.Join("testdata", "vectors.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read the conformance vectors at %s: %v", path, err)
	}
	var v vectorFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("vectors.json does not parse: %v", err)
	}
	// A missing or moved file must fail rather than turn every case below into
	// a loop over nothing, which would pass.
	if len(v.Accepted) < 10 || len(v.Rejected) < 3 {
		t.Fatalf("vector file looks truncated: %d accepted, %d rejected",
			len(v.Accepted), len(v.Rejected))
	}
	return v
}

func TestAcceptedVectorsCanonicaliseToTheirLiteral(t *testing.T) {
	for _, c := range loadVectors(t).Accepted {
		got, err := CanonicalReceipt(c.Value)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.Name, err)
			continue
		}
		if string(got) != c.Canonical {
			t.Errorf("%s:\n  got  %s\n  want %s", c.Name, got, c.Canonical)
		}
	}
}

func TestRejectedVectorsThisRuntimeCanStillSee(t *testing.T) {
	// JSON does not distinguish 1.0 from 1, so by the time a value has been
	// through encoding/json the evidence for some cases is gone: 1.0 arrives as
	// float64(1), and 9007199254740993 arrives already rounded. Those are the
	// signer's to refuse. Only the genuinely fractional case is visible here.
	seen := 0
	for _, c := range loadVectors(t).Rejected {
		visible := false
		for _, lang := range c.DetectableBy {
			if lang == "go" {
				visible = true
			}
		}
		if !visible {
			continue
		}
		seen++
		if _, err := CanonicalReceipt(c.Value); err == nil {
			t.Errorf("%s: expected a refusal, got none", c.Name)
		}
	}
	if seen == 0 {
		t.Fatal("no rejected vector was marked detectable in go, so this test " +
			"asserted nothing")
	}
}

func TestAValueTheParserAlreadyCorruptedCannotBeCaughtHere(t *testing.T) {
	// Asserted rather than described, so the claim in the vector file is a fact
	// about this runtime rather than a comment somebody believed.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(`{"a":9007199254740993}`), &parsed); err != nil {
		t.Fatal(err)
	}
	if got := parsed["a"].(float64); got != 9007199254740992 {
		t.Fatalf("expected the parser to round to ...992, got %v", got)
	}
}

func TestAngleBracketsAreNotHTMLEscaped(t *testing.T) {
	// encoding/json escapes <, > and & by default, which the published rule
	// forbids. The receipt `type` field is a URL, so this is one edit away from
	// mattering.
	got, err := CanonicalReceipt(map[string]any{"a": "<b>&c"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `\u003c`) || strings.Contains(string(got), `\u0026`) {
		t.Fatalf("HTML escaping leaked into the canonical form: %s", got)
	}
	if string(got) != `{"a":"<b>&c"}` {
		t.Fatalf("got %s", got)
	}
}

func b64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func signedReceipt(t *testing.T) (ReceiptDocument, KeyDocument) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"type":   "https://agoreum.xyz/schemas/settlement-receipt-v1",
		"issuer": "agoreum.xyz",
		"settlement": map[string]any{
			"chain_id":         float64(84532),
			"transaction_hash": "0xabc",
			"amount":           "1.025000",
		},
	}
	message, err := CanonicalReceipt(payload)
	if err != nil {
		t.Fatal(err)
	}
	kid := b64(pub)[:16]
	return ReceiptDocument{
			Receipt:   payload,
			Signature: b64(ed25519.Sign(priv, message)),
			KeyID:     kid,
		}, KeyDocument{
			Keys: []JWK{{Kty: "OKP", Crv: "Ed25519", Kid: kid, X: b64(pub)}},
		}
}

func TestAGenuineReceiptVerifies(t *testing.T) {
	doc, keys := signedReceipt(t)
	r := VerifyReceipt(doc, keys)
	if !r.SignatureValid {
		t.Fatalf("expected valid, got %q", r.Reason)
	}
	// The whole design rests on this being two findings rather than one.
	if r.TransactionHash != "0xabc" || r.ChainID != 84532 {
		t.Fatalf("chain evidence not reported: %+v", r)
	}
	if !strings.Contains(r.StillToVerify(), "on chain") {
		t.Fatal("the result does not say the chain half is still owed")
	}
}

func TestATamperedAmountDoesNotVerify(t *testing.T) {
	// The case that matters: the signature must cover the settlement figures,
	// not merely some payload.
	doc, keys := signedReceipt(t)
	doc.Receipt["settlement"].(map[string]any)["amount"] = "9999.000000"
	if VerifyReceipt(doc, keys).SignatureValid {
		t.Fatal("a tampered amount verified")
	}
}

func TestAnUnknownKeyIDIsRefused(t *testing.T) {
	// A forger supplying their own key document is the obvious attack.
	doc, keys := signedReceipt(t)
	keys.Keys[0].Kid = "not-the-one"
	r := VerifyReceipt(doc, keys)
	if r.SignatureValid || !strings.Contains(r.Reason, "kid") {
		t.Fatalf("expected a kid refusal, got %+v", r)
	}
}

func TestAnUnsignedReceiptIsNotTreatedAsValid(t *testing.T) {
	doc, keys := signedReceipt(t)
	doc.Signature = ""
	r := VerifyReceipt(doc, keys)
	if r.SignatureValid || !strings.Contains(r.Reason, "no signature") {
		t.Fatalf("expected a missing-signature refusal, got %+v", r)
	}
}
