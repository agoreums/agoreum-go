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
}

// StillToVerify names the half this function did not do.
func (r ReceiptVerification) StillToVerify() string {
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
type ReceiptDocument struct {
	Receipt   map[string]any `json:"receipt"`
	Signature string         `json:"signature"`
	KeyID     string         `json:"key_id"`
}

// VerifyReceipt checks a receipt's signature against a fetched key document.
//
// Fetch the key document yourself rather than trusting a copy handed over with
// the receipt, which would let a forger supply both halves.
func VerifyReceipt(doc ReceiptDocument, keys KeyDocument) ReceiptVerification {
	result := ReceiptVerification{KeyID: doc.KeyID}
	if settlement, ok := doc.Receipt["settlement"].(map[string]any); ok {
		if tx, ok := settlement["transaction_hash"].(string); ok {
			result.TransactionHash = tx
		}
		if id, ok := settlement["chain_id"].(float64); ok {
			result.ChainID = int64(id)
		}
	}

	if doc.Receipt == nil {
		result.Reason = "the document has no receipt object"
		return result
	}
	if doc.Signature == "" {
		result.Reason = "the receipt carries no signature, so it attributes to nobody"
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

	message, err := CanonicalReceipt(doc.Receipt)
	if err != nil {
		// A receipt Agoreum could not have produced. Its signer refuses these,
		// so one arriving means a forgery attempt or a bug.
		result.Reason = "the receipt is not canonicalisable: " + err.Error()
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
		result.Reason = "the signature does not verify over the receipt's canonical form"
		return result
	}

	result.SignatureValid = true
	return result
}
