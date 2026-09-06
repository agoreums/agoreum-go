// Verifying an Agoreum settlement receipt without trusting Agoreum.
//
// A receipt is a signed claim plus the coordinates to check it. The signature
// proves Agoreum said something; following TransactionHash on the named chain is
// what proves it is true. This file does the first half and says plainly that it
// has only done the first half.
//
// It is written from the published rule rather than from Agoreum's signing code,
// which is the only thing that makes agreement meaningful: an implementation
// derived from the specification producing identical bytes shows the
// specification is implementable by somebody who has only the documentation.
// Every ambiguity found in that rule so far was found this way, by writing
// another implementation and watching it disagree.
//
// No dependencies. Ed25519 and JSON are both in the standard library.
package agoreum

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MaxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER. Past this an integer
// cannot survive a round trip through a JSON parser backed by a double, which
// most are, so Agoreum's signer refuses to put one in a receipt.
const MaxSafeInteger = 1<<53 - 1

// ErrNotCanonicalisable reports a payload with no single canonical form that
// every language agrees on. Returned rather than guessed at: producing bytes
// anyway would give a digest some other correct implementation disagrees with,
// and the visible symptom would be a valid receipt looking forged.
type ErrNotCanonicalisable struct {
	Path   string
	Reason string
}

func (e *ErrNotCanonicalisable) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Reason)
}

// CanonicalReceipt returns the exact bytes an Agoreum receipt is signed over.
//
// The rule, as published in the key document:
//
//   - keys sorted at every level by Unicode code point
//   - no whitespace between tokens
//   - UTF-8
//   - no \u escaping of non-ASCII characters
//   - every number an integer within +/-(2^53-1), written plainly
//
// Written by hand rather than with encoding/json's marshaller, for two reasons
// that both matter. json.Marshal escapes <, > and & to < and friends by
// default, which the rule forbids, and SetEscapeHTML(false) only reaches the
// Encoder. And Go maps have no order, so key sorting has to be explicit anyway.
//
// Sorting Go strings compares bytes, and for valid UTF-8 byte order is code
// point order, so sort.Strings is the right collation here. That is not true of
// JavaScript, which compares UTF-16 code units and disagrees above U+FFFF.
func CanonicalReceipt(value any) ([]byte, error) {
	var b strings.Builder
	if err := writeCanonical(&b, value, "receipt"); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func writeCanonical(b *strings.Builder, value any, path string) error {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		s, err := canonicalString(v)
		if err != nil {
			return err
		}
		b.WriteString(s)
	case float64:
		// Every number from encoding/json arrives as float64, so integrality is
		// checked rather than assumed. A receipt carrying a genuine fraction has
		// no canonical form: Go writes 1 where Python writes 1.0.
		if v != float64(int64(v)) {
			return &ErrNotCanonicalisable{path, fmt.Sprintf(
				"%v is not an integer, and a fractional number has no canonical "+
					"form across languages. Agoreum carries fractional quantities "+
					"as decimal strings", v)}
		}
		if v > MaxSafeInteger || v < -MaxSafeInteger {
			return &ErrNotCanonicalisable{path, fmt.Sprintf(
				"%v is outside the safe integer range, so a JSON parser using "+
					"doubles has already read a different number than the signer "+
					"wrote", v)}
		}
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case json.Number:
		b.WriteString(v.String())
	case int:
		b.WriteString(strconv.Itoa(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			s, err := canonicalString(k)
			if err != nil {
				return err
			}
			b.WriteString(s)
			b.WriteByte(':')
			if err := writeCanonical(b, v[k], path+"."+k); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	default:
		return &ErrNotCanonicalisable{path, fmt.Sprintf(
			"%T is not JSON and has no canonical form", value)}
	}
	return nil
}

// canonicalString encodes a JSON string with the standard escapes and nothing
// more.
//
// json.Marshal additionally escapes <, > and & to their \u form, which the
// published rule forbids: the receipt's `type` field is a URL and the rule says
// non-ASCII is not escaped and nothing says these are. Only the Encoder exposes
// SetEscapeHTML, so this goes through one rather than through Marshal.
//
// The first version tried to undo the escaping with string replacement and was
// a no-op, because the replacement pairs were written as literals rather than
// as the escape sequences. TestAngleBracketsAreNotHTMLEscaped caught it.
func canonicalString(s string) (string, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	// Encode appends a newline; the canonical form has no insignificant
	// whitespace anywhere.
	return strings.TrimRight(buf.String(), "\n"), nil
}

// ReceiptVerification separates what was checked from what was not.
//
// SignatureValid means Agoreum signed this exact payload. It does not mean the
// settlement happened. Nothing here reads the chain, and reporting one finding
// where there are two is how a signature check gets mistaken for proof of
// payment.
type ReceiptVerification struct {
	SignatureValid  bool
	KeyID           string
	Reason          string
	TransactionHash string
	ChainID         int64
	// DocumentType is "receipt" or "attestation". Reported because the two
	// need different things done next: a receipt names one transaction, an
	// attestation names an address whose whole history has to be counted.
	DocumentType string
}

// StillToVerify names the half this function did not do.
//
// Specific to the document type. A generic sentence would be the same defect
// the attestation itself shipped with: instructions that look followable and
// send the reader somewhere useless. Naming only EscrowReleased is the
// particular way to be wrong, because a dispute settled by the arbiter emits
// EscrowSettled, which carries no provider address, and those orders are
// counted in the attestation.
func (r ReceiptVerification) StillToVerify() string {
	if r.DocumentType == "attestation" {
		return fmt.Sprintf(
			"The signature only shows Agoreum made this claim. Count the "+
				"settlements yourself on chain %d: filter EscrowCreated by its "+
				"indexed provider topic equal to the agent's payout_address, then "+
				"count the resulting escrow ids that later emitted EscrowReleased "+
				"or EscrowSettled. Both matter, because a dispute settled by the "+
				"arbiter emits EscrowSettled, which carries no provider address, "+
				"and those orders are counted in the attestation.",
			r.ChainID)
	}
	return fmt.Sprintf(
		"The signature only shows Agoreum made this claim. Confirm transaction "+
			"%s on chain %d before treating the settlement as real.",
		r.TransactionHash, r.ChainID)
}

// JWK is one key from the document at /.well-known/agoreum-receipts.json.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Kid string `json:"kid"`
	X   string `json:"x"`
}

// KeyDocument is that document.
type KeyDocument struct {
	Keys []JWK `json:"keys"`
}

// ReceiptDocument is the envelope the API returns.
//
// A settlement receipt and a reputation attestation are the same object: same
// key, same canonical form, same key document. Only the payload field differs,
// so both are accepted rather than duplicating a check whose hard half,
// CanonicalReceipt, took three separate ambiguities to get right.
type ReceiptDocument struct {
	Receipt     map[string]any `json:"receipt,omitempty"`
	Attestation map[string]any `json:"attestation,omitempty"`
	Signature   string         `json:"signature"`
	KeyID       string         `json:"key_id"`
}

// VerifyReceipt checks a receipt's signature against a fetched key document.
//
// Fetch the key document yourself rather than trusting a copy handed over with
// the receipt, which would let a forger supply both halves.
func VerifyReceipt(doc ReceiptDocument, keys KeyDocument) ReceiptVerification {
	result := ReceiptVerification{KeyID: doc.KeyID, DocumentType: "receipt"}
	payload := doc.Receipt
	if doc.Attestation != nil {
		result.DocumentType = "attestation"
		payload = doc.Attestation
		if basis, ok := doc.Attestation["basis"].(map[string]any); ok {
			if id, ok := basis["chain_id"].(float64); ok {
				result.ChainID = int64(id)
			}
		}
	} else if settlement, ok := doc.Receipt["settlement"].(map[string]any); ok {
		if tx, ok := settlement["transaction_hash"].(string); ok {
			result.TransactionHash = tx
		}
		if id, ok := settlement["chain_id"].(float64); ok {
			result.ChainID = int64(id)
		}
	}

	if payload == nil {
		result.Reason = "the document has no " + result.DocumentType + " object"
		return result
	}
	if doc.Signature == "" {
		result.Reason = "the " + result.DocumentType +
			" carries no signature, so it attributes to nobody"
		return result
	}

	var jwk *JWK
	for i := range keys.Keys {
		if keys.Keys[i].Kid == doc.KeyID {
			jwk = &keys.Keys[i]
			break
		}
	}
	if jwk == nil {
		result.Reason = fmt.Sprintf(
			"the key document publishes no key with kid %q, so this signature "+
				"cannot be attributed to Agoreum", doc.KeyID)
		return result
	}

	message, err := CanonicalReceipt(payload)
	if err != nil {
		// A document Agoreum could not have produced. Its signer refuses these,
		// so one arriving means a forgery attempt or a bug.
		result.Reason = "the " + result.DocumentType +
			" is not canonicalisable: " + err.Error()
		return result
	}

	pub, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(jwk.X, "="))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		result.Reason = "the published key is not a usable Ed25519 public key"
		return result
	}
	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(doc.Signature, "="))
	if err != nil {
		result.Reason = "the signature is not valid base64url"
		return result
	}

	if !ed25519.Verify(ed25519.PublicKey(pub), message, sig) {
		result.Reason = "the signature does not verify over the " +
			result.DocumentType + "'s canonical form"
		return result
	}

	result.SignatureValid = true
	return result
}

// AgoreumDID is the DID Agoreum signs x402 receipts under.
//
// Pin it rather than reading it out of the receipt, for the reason set out on
// VerifyX402.
const AgoreumDID = "did:web:agoreum.xyz"

// compactJWS matches a JWS Compact Serialization: three base64url segments.
//
// Matched strictly rather than split on ".", because base64url contains no dot
// and a string that is nearly one should be refused by name rather than fail
// later as a bad signature, which reads like forgery instead of bad input.
var compactJWS = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

// PublicKeyJWK is the key material inside a DID verification method.
type PublicKeyJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
}

// VerificationMethod is one entry from a DID document's verificationMethod.
type VerificationMethod struct {
	ID           string       `json:"id"`
	Type         string       `json:"type,omitempty"`
	Controller   string       `json:"controller,omitempty"`
	PublicKeyJWK PublicKeyJWK `json:"publicKeyJwk"`
}

// DIDDocument is the JSON served at a did:web identifier's resolution URL.
//
// AssertionMethod is []any because DID core allows either a reference string or
// an embedded verification method, and a struct that accepts only the string
// form would reject a conformant document.
type DIDDocument struct {
	ID                 string               `json:"id"`
	VerificationMethod []VerificationMethod `json:"verificationMethod"`
	AssertionMethod    []any                `json:"assertionMethod"`
}

// X402Verification reports what an x402 receipt's signature established, and
// what it did not.
//
// Same split as ReceiptVerification and for the same reason: the signature
// shows Agoreum made the claim, the chain shows the money moved, and collapsing
// the two into one boolean is how a signature check gets mistaken for proof of
// payment.
type X402Verification struct {
	SignatureValid bool
	KeyID          string
	Reason         string
	Payload        map[string]any
	Transaction    string
	ChainID        int64
	Payer          string
	ResourceURL    string
}

// StillToVerify names the half this function did not do.
func (r X402Verification) StillToVerify() string {
	if !r.SignatureValid {
		return "Nothing was established. The signature did not verify."
	}
	if r.Transaction == "" {
		return "The signature shows Agoreum made this claim, but the receipt " +
			"names no transaction, so there is nothing on chain to check it " +
			"against. Treat it as unsettled."
	}
	return fmt.Sprintf(
		"The signature only shows Agoreum made this claim. Confirm transaction "+
			"%s on chain %d before treating the settlement as real.",
		r.Transaction, r.ChainID)
}

// DIDWebURL returns the HTTPS URL a did:web identifier resolves to.
//
// Pure and offline, so the resolution rule is something an integrator reads
// rather than guesses, and so it can be tested without a network:
//
//	did:web:agoreum.xyz       -> https://agoreum.xyz/.well-known/did.json
//	did:web:example.com:a:b   -> https://example.com/a/b/did.json
//	did:web:localhost%3A8080  -> https://localhost:8080/.well-known/did.json
//
// The last case is the one worth having a function for: a port lives in the DID
// percent-encoded, and a reader who splits on ":" without decoding gets a host
// of localhost and a path segment of 8080.
func DIDWebURL(did string) (string, error) {
	if !strings.HasPrefix(did, "did:web:") {
		return "", fmt.Errorf("not a did:web identifier: %q", did)
	}
	parts := strings.Split(strings.TrimPrefix(did, "did:web:"), ":")
	host, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", fmt.Errorf("the did:web host is not valid percent-encoding: %q", did)
	}
	if host == "" {
		return "", fmt.Errorf("the did:web identifier names no host: %q", did)
	}
	segments := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return "", fmt.Errorf("a did:web path segment is not valid percent-encoding: %q", did)
		}
		segments = append(segments, decoded)
	}
	if len(segments) == 0 {
		return "https://" + host + "/.well-known/did.json", nil
	}
	return "https://" + host + "/" + strings.Join(segments, "/") + "/did.json", nil
}

func decodeJWSSegment(segment string) (map[string]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(segment, "="))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("the segment is not a JSON object")
	}
	return out, nil
}

// VerifyX402 checks an x402 receipt against a resolved DID document.
//
// compact is the signature field of GET /orders/{id}/receipt/x402, a JWS
// Compact Serialization. doc is the JSON that DIDWebURL(expectDID) serves.
// Resolve it yourself: a document handed over with the receipt lets a forger
// supply both halves. Pass "" for expectDID to mean AgoreumDID.
//
// expectDID is what makes the rest mean anything, and it is the check an
// implementation is most likely to leave out. A receipt names its own signer in
// kid. Verifying it against whatever document that DID resolves to proves only
// that somebody signed something with their own key, which any forger can do by
// publishing a DID document on a domain they control: the key signs, the
// document publishes it, assertionMethod authorises it, and every other check
// here passes. Pinning the DID is what turns a valid signature into a statement
// by Agoreum specifically.
//
// Two lines implement it, and they are not equally load-bearing. Requiring
// doc.ID to equal expectDID is the one that closes the hole, because it refuses
// a document that was never Agoreum's whatever the receipt claims. Comparing
// kid's own DID is redundant for safety and kept for the message: without it a
// receipt from somebody else is refused for having the wrong document rather
// than for having the wrong signer, and the reader is sent looking in the wrong
// place. Said plainly here because a comment that credits the wrong line for a
// security property is how the line that matters gets removed later as
// duplication.
//
// The key must be listed under assertionMethod. Agoreum publishes it there and
// deliberately not under authentication: it makes claims about settlements that
// already happened, holds no funds, and proves nothing about who is making a
// request. A verifier that accepts any key in the document discards that
// distinction, so this one does not.
func VerifyX402(compact string, doc DIDDocument, expectDID string) X402Verification {
	if expectDID == "" {
		expectDID = AgoreumDID
	}
	var result X402Verification

	failed := func(reason string) X402Verification {
		result.SignatureValid = false
		result.Reason = reason
		return result
	}

	if !compactJWS.MatchString(compact) {
		return failed("this is not a JWS Compact Serialization: three base64url " +
			"segments separated by dots were expected")
	}
	segments := strings.Split(compact, ".")
	headerSegment, payloadSegment, signatureSegment := segments[0], segments[1], segments[2]

	header, err := decodeJWSSegment(headerSegment)
	if err != nil {
		return failed("a segment does not decode to JSON: " + err.Error())
	}
	payload, err := decodeJWSSegment(payloadSegment)
	if err != nil {
		return failed("a segment does not decode to JSON: " + err.Error())
	}

	keyID, _ := header["kid"].(string)
	result.KeyID = keyID

	// RFC 7515: crit names extensions the verifier must understand. This one
	// understands none, so the only correct response is refusal. Accepting an
	// unknown critical header is how a signature stays valid while meaning
	// something other than what was read.
	if _, ok := header["crit"]; ok {
		return failed("the header declares critical extensions this verifier " +
			"does not implement, so the receipt cannot be safely interpreted")
	}
	if alg, _ := header["alg"].(string); alg != "EdDSA" {
		return failed(fmt.Sprintf("the header names algorithm %q; Agoreum signs "+
			"with EdDSA and nothing else is accepted here", header["alg"]))
	}
	if keyID == "" || !strings.Contains(keyID, "#") {
		return failed("the header carries no kid naming a verification method, " +
			"so the signature cannot be attributed to a key")
	}

	did := strings.SplitN(keyID, "#", 2)[0]
	if did != expectDID {
		return failed(fmt.Sprintf("the receipt is signed by %q, not %q. A valid "+
			"signature by somebody else is not a statement by Agoreum", did, expectDID))
	}
	if doc.ID != expectDID {
		return failed(fmt.Sprintf("the DID document identifies itself as %q, not "+
			"%q, so it is not the right document to check this receipt against",
			doc.ID, expectDID))
	}

	// Existence before authority, so the two failures read differently: a key
	// nobody publishes is a rotation or a forgery, a key published but not
	// authorised is a different problem entirely, and one message for both
	// sends the reader looking in the wrong place.
	var method *VerificationMethod
	for i := range doc.VerificationMethod {
		if doc.VerificationMethod[i].ID == keyID {
			method = &doc.VerificationMethod[i]
			break
		}
	}
	if method == nil {
		return failed(fmt.Sprintf(
			"the DID document publishes no verification method with id %s", keyID))
	}

	asserting := false
	for _, entry := range doc.AssertionMethod {
		switch value := entry.(type) {
		case string:
			asserting = asserting || value == keyID
		case map[string]any:
			id, _ := value["id"].(string)
			asserting = asserting || id == keyID
		}
	}
	if !asserting {
		return failed(fmt.Sprintf("%s is not listed under assertionMethod, so "+
			"this key is not authorised to make claims even if the document "+
			"publishes it", keyID))
	}
	jwk := method.PublicKeyJWK
	if jwk.Kty != "OKP" || jwk.Crv != "Ed25519" || jwk.X == "" {
		return failed("the verification method does not carry an Ed25519 public " +
			"key in publicKeyJwk")
	}

	pub, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(jwk.X, "="))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return failed("the published key is not a usable Ed25519 public key")
	}
	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(signatureSegment, "="))
	if err != nil {
		return failed("the signature is not valid base64url")
	}

	// The JWS signing input, per RFC 7515: the two encoded segments joined by a
	// dot, as ASCII. Not the canonical JSON. Re-encoding the parsed payload
	// would reject any receipt whose producer serialised it even slightly
	// differently, which is the whole reason JWS carries its own payload.
	signingInput := []byte(headerSegment + "." + payloadSegment)
	if !ed25519.Verify(ed25519.PublicKey(pub), signingInput, sig) {
		return failed("the signature does not verify over the signing input")
	}

	result.SignatureValid = true
	result.Payload = payload
	result.Transaction, _ = payload["transaction"].(string)
	result.Payer, _ = payload["payer"].(string)
	result.ResourceURL, _ = payload["resourceUrl"].(string)
	if network, ok := payload["network"].(string); ok {
		if rest, found := strings.CutPrefix(network, "eip155:"); found {
			if id, err := strconv.ParseInt(rest, 10, 64); err == nil {
				result.ChainID = id
			}
		}
	}
	return result
}
