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
	X402 x402Vectors `json:"x402"`
}

type x402Vectors struct {
	DID         string      `json:"did"`
	DIDDocument DIDDocument `json:"did_document"`
	DIDWebURLs  []struct {
		DID string `json:"did"`
		URL string `json:"url"`
		Why string `json:"why"`
	} `json:"did_web_urls"`
	Accepted []struct {
		Name        string         `json:"name"`
		Segments    []string       `json:"segments"`
		Payload     map[string]any `json:"payload"`
		ChainID     int64          `json:"chain_id"`
		Transaction string         `json:"transaction"`
		Payer       string         `json:"payer"`
		ResourceURL string         `json:"resource_url"`
	} `json:"accepted"`
	Rejected []struct {
		Name         string   `json:"name"`
		Segments     []string `json:"segments"`
		Why          string   `json:"why"`
		ExpectReason string   `json:"expect_reason"`
		// DIDDocument is a pointer so that "absent" and "an empty document"
		// stay distinguishable: most cases fall back to the shared document,
		// and three deliberately supply a broken one.
		DIDDocument *DIDDocument `json:"did_document"`
		ExpectDID   string       `json:"expect_did"`
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

// A settlement receipt and a reputation attestation are the same object: same
// key, same canonical form, same key document. Verifying one and not the other
// would mean two implementations of an identical check, and the hard half of
// it, CanonicalReceipt, took three separate ambiguities to get right. What
// differs is what remains unproven afterwards.
func signedAttestation(t *testing.T) (ReceiptDocument, KeyDocument) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"type":   "https://agoreum.xyz/schemas/reputation-attestation-v1",
		"issuer": "agoreum.xyz",
		"agent": map[string]any{
			"slug":           "an-agent",
			"payout_address": "0x1111111111111111111111111111111111111111",
		},
		"basis": map[string]any{
			"chain_id":        float64(84532),
			"escrow_contract": "0x2222222222222222222222222222222222222222",
		},
		"settled": map[string]any{"completed_orders": float64(3)},
	}
	message, err := CanonicalReceipt(payload)
	if err != nil {
		t.Fatal(err)
	}
	kid := b64(pub)[:16]
	return ReceiptDocument{
			Attestation: payload,
			Signature:   b64(ed25519.Sign(priv, message)),
			KeyID:       kid,
		}, KeyDocument{
			Keys: []JWK{{Kty: "OKP", Crv: "Ed25519", Kid: kid, X: b64(pub)}},
		}
}

func TestAGenuineAttestationVerifies(t *testing.T) {
	doc, keys := signedAttestation(t)

	result := VerifyReceipt(doc, keys)

	if !result.SignatureValid {
		t.Fatalf("a genuine attestation did not verify: %s", result.Reason)
	}
	if result.DocumentType != "attestation" {
		t.Errorf("document type = %q, want attestation", result.DocumentType)
	}
	if result.ChainID != 84532 {
		t.Errorf("chain = %d, want 84532", result.ChainID)
	}
	// A receipt names one transaction; an attestation names none, and telling
	// a reader to "confirm transaction " is an instruction to nowhere.
	if result.TransactionHash != "" {
		t.Errorf("transaction hash = %q, want empty", result.TransactionHash)
	}
}

func TestAnInflatedAttestationIsRefused(t *testing.T) {
	// The edit somebody would actually make.
	doc, keys := signedAttestation(t)
	doc.Attestation["settled"].(map[string]any)["completed_orders"] = float64(9999)

	result := VerifyReceipt(doc, keys)

	if result.SignatureValid {
		t.Fatal("an inflated settled count still verified")
	}
	if !strings.Contains(result.Reason, "attestation") {
		t.Errorf("reason %q does not name the document type", result.Reason)
	}
}

func TestAttestationGuidanceSendsTheReaderSomewhereThatWorks(t *testing.T) {
	doc, keys := signedAttestation(t)

	guidance := VerifyReceipt(doc, keys).StillToVerify()

	for _, needed := range []string{"EscrowCreated", "payout_address"} {
		if !strings.Contains(guidance, needed) {
			t.Errorf("guidance never mentions %s: %s", needed, guidance)
		}
	}
	// The counting step itself must name both events, not merely the sentence
	// explaining why. Checking for the phrase is what makes the instruction the
	// thing under test: a reader who filters only EscrowReleased undercounts
	// arbitrated disputes and concludes the attestation was inflated.
	if !strings.Contains(guidance, "EscrowReleased or EscrowSettled") {
		t.Errorf("the counting instruction names one settlement event: %s", guidance)
	}
}

func TestAReceiptStillGetsReceiptGuidance(t *testing.T) {
	// The control. The change must not have quietly rewritten both paths.
	doc, keys := signedReceipt(t)

	result := VerifyReceipt(doc, keys)

	if result.DocumentType != "receipt" {
		t.Errorf("document type = %q, want receipt", result.DocumentType)
	}
	guidance := result.StillToVerify()
	if !strings.Contains(guidance, "0xabc") {
		t.Errorf("receipt guidance lost its transaction: %s", guidance)
	}
	if strings.Contains(guidance, "EscrowCreated") {
		t.Errorf("receipt guidance took the attestation path: %s", guidance)
	}
}

// compactOf joins a fixture's segments back into a JWS Compact
// Serialization.
//
// The vectors store segments rather than whole JWSs so that nothing in the
// repository is JWT-shaped, which keeps the secret scan's full ruleset over a
// file that also carries private key seeds. The block's own comment explains
// it; this is the other half.
func compactOf(segments []string) string {
	return strings.Join(segments, ".")
}

func loadX402(t *testing.T) x402Vectors {
	t.Helper()
	block := loadVectors(t).X402
	// Same guard as loadVectors, for the same reason. Every case below ranges
	// over a slice out of this block, so a rename turns the whole set into zero
	// assertions and the package still reports pass.
	if len(block.Accepted) < 2 || len(block.Rejected) < 10 || len(block.DIDWebURLs) < 3 {
		t.Fatalf("the x402 vectors look truncated: %d accepted, %d rejected, %d urls",
			len(block.Accepted), len(block.Rejected), len(block.DIDWebURLs))
	}
	if block.DIDDocument.ID != block.DID {
		t.Fatalf("the fixture document identifies itself as %q, not %q",
			block.DIDDocument.ID, block.DID)
	}
	return block
}

func TestDIDWebResolvesToThePublishedURL(t *testing.T) {
	for _, c := range loadX402(t).DIDWebURLs {
		got, err := DIDWebURL(c.DID)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.DID, err)
			continue
		}
		if got != c.URL {
			t.Errorf("%s:\n  got  %s\n  want %s\n  %s", c.DID, got, c.URL, c.Why)
		}
	}
}

func TestADIDThatIsNotDIDWebIsRefused(t *testing.T) {
	if _, err := DIDWebURL("did:key:z6Mk"); err == nil {
		t.Error("did:key was accepted as a did:web identifier")
	}
}

func TestAcceptedX402ReceiptsVerify(t *testing.T) {
	block := loadX402(t)
	for _, c := range block.Accepted {
		result := VerifyX402(compactOf(c.Segments), block.DIDDocument, block.DID)
		if !result.SignatureValid {
			t.Errorf("%s: refused with %q", c.Name, result.Reason)
			continue
		}
		if result.Transaction != c.Transaction {
			t.Errorf("%s: transaction = %q, want %q", c.Name, result.Transaction, c.Transaction)
		}
		if result.ChainID != c.ChainID {
			t.Errorf("%s: chain = %d, want %d", c.Name, result.ChainID, c.ChainID)
		}
		if result.Payer != c.Payer {
			t.Errorf("%s: payer = %q, want %q", c.Name, result.Payer, c.Payer)
		}
		if result.ResourceURL != c.ResourceURL {
			t.Errorf("%s: resourceUrl = %q, want %q", c.Name, result.ResourceURL, c.ResourceURL)
		}
	}
}

func TestAReceiptNamingNoTransactionSaysItProvesNoSettlement(t *testing.T) {
	// A valid signature over a receipt with no transaction is still a valid
	// signature, and reporting only SignatureValid lets the reader conclude the
	// money moved. The two accepted vectors exist so that collapsing the
	// distinction fails loudly.
	block := loadX402(t)
	var settled, unsettled X402Verification
	for _, c := range block.Accepted {
		result := VerifyX402(compactOf(c.Segments), block.DIDDocument, block.DID)
		if c.Transaction == "" {
			unsettled = result
		} else {
			settled = result
		}
	}
	if !settled.SignatureValid || !unsettled.SignatureValid {
		t.Fatal("both fixtures carry genuine signatures and both must verify")
	}
	if unsettled.Transaction != "" {
		t.Fatalf("the unsettled fixture names transaction %q", unsettled.Transaction)
	}
	if !strings.Contains(settled.StillToVerify(), settled.Transaction) {
		t.Errorf("settled guidance lost its transaction: %s", settled.StillToVerify())
	}
	if !strings.Contains(unsettled.StillToVerify(), "unsettled") {
		t.Errorf("unsettled guidance does not say so: %s", unsettled.StillToVerify())
	}
	if settled.StillToVerify() == unsettled.StillToVerify() {
		t.Error("both receipts got identical guidance, so the distinction is gone")
	}
}

func TestRejectedX402ReceiptsAreRefused(t *testing.T) {
	block := loadX402(t)
	for _, c := range block.Rejected {
		document := block.DIDDocument
		if c.DIDDocument != nil {
			document = *c.DIDDocument
		}
		expect := c.ExpectDID
		if expect == "" {
			expect = block.DID
		}
		result := VerifyX402(compactOf(c.Segments), document, expect)
		if result.SignatureValid {
			t.Errorf("%s was accepted: %s", c.Name, c.Why)
			continue
		}
		// Refused is not enough, and asserting only that is what hid a real
		// defect. Three of these vectors were once refused by the signature
		// check rather than by the check they are named after, because editing
		// a JWS header breaks the signature. Deleting the algorithm and
		// extension checks outright left this test green. The reason has to
		// match.
		if !strings.Contains(result.Reason, c.ExpectReason) {
			t.Errorf("%s was refused for the wrong reason:\n  expected to mention: %s\n  actually said:       %s",
				c.Name, c.ExpectReason, result.Reason)
		}
	}
}

func TestTheRealAgoreumDIDIsTheDefault(t *testing.T) {
	// A verifier whose safety depends on an argument the caller supplies is
	// safe only for callers who read the documentation. Passing "" must mean
	// the real DID, not "whatever this receipt claims".
	block := loadX402(t)
	if AgoreumDID != "did:web:agoreum.xyz" {
		t.Fatalf("AgoreumDID = %q", AgoreumDID)
	}
	result := VerifyX402(compactOf(block.Accepted[0].Segments), block.DIDDocument, "")
	if result.SignatureValid {
		t.Error("a fixture signed by did:web:vectors.example passed as Agoreum")
	}
	if !strings.Contains(result.Reason, "agoreum.xyz") {
		t.Errorf("the refusal does not name the expected signer: %s", result.Reason)
	}
}
