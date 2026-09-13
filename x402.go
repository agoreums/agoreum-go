package agoreum

// Paying an order on the x402 auth-capture rail.
//
// Three separate things happen, and the SDK keeps them separate because a
// program that treats any one of them as "paid" will be wrong sometimes:
//
//  1. Authorization. The buyer's wallet signs an EIP-712 message (an ERC-3009
//     ReceiveWithAuthorization) that lets Base's AuthCaptureEscrow pull an exact
//     amount of USDC into a hold under Agoreum's operator contract, for one
//     order, until a stated expiry. Nothing moves when it is signed.
//  2. Submission. The signed authorization is posted to the API, which checks it
//     against the order and relays it to the operator. The response says what
//     the relay did ("collected", "submitted", "already_collected"). Nothing
//     about the order changes because of this response.
//  3. On-chain settlement. The chain confirms the hold, the indexer sees the
//     event past the confirmation depth, and the order becomes "funded". That is
//     the only fact that matters, and it is read from the order, not from step 2.
//
// The SDK never holds a key. It takes a signing function and hands it the exact
// typed data to sign, after checking that the typed data says what the plain
// fields of the instructions say. It does not recompute the message hash (no
// keccak in the standard library) and does not claim to; the wallet shows the
// message, the operator contract checks every field, and the API's tests
// cross-check the hash against the deployed contracts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
)

const (
	// X402Scheme is the x402 scheme this rail uses.
	X402Scheme = "auth-capture"
	// X402Version is the x402 wire version this rail uses.
	X402Version = 2
)

// SignTypedData signs an EIP-712 document and returns the 65-byte signature as
// 0x-prefixed hex. The SDK never sees a key; this is the caller's wallet.
type SignTypedData func(ctx context.Context, typedData EIP712TypedData) (string, error)

// EIP712TypedData is the document a wallet signs, in the eth_signTypedData_v4 shape.
type EIP712TypedData struct {
	Types       map[string][]EIP712Field `json:"types"`
	PrimaryType string                   `json:"primaryType"`
	Domain      EIP712Domain             `json:"domain"`
	Message     map[string]string        `json:"message"`
}

// EIP712Field is one typed field in an EIP-712 type definition.
type EIP712Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// EIP712Domain is the signing domain: the token's name and version, the chain,
// and the token address.
type EIP712Domain struct {
	Name              string `json:"name"`
	Version           string `json:"version"`
	ChainID           int64  `json:"chainId"`
	VerifyingContract string `json:"verifyingContract"`
}

// X402Offer is one entry of an x402 PaymentRequired's accepts list.
type X402Offer struct {
	Scheme            string         `json:"scheme"`
	Network           string         `json:"network"`
	Amount            string         `json:"amount"`
	Asset             string         `json:"asset"`
	PayTo             string         `json:"payTo"`
	MaxTimeoutSeconds int64          `json:"maxTimeoutSeconds"`
	Extra             map[string]any `json:"extra"`
}

// X402PaymentInstructions is the API's payment instructions for an order on the
// x402 rail. Raw preserves the full document.
type X402PaymentInstructions struct {
	OrderID                  string `json:"order_id"`
	OrderRef                 string `json:"order_reference"`
	SettlementRail           string `json:"settlement_rail"`
	ChainID                  int64  `json:"chain_id"`
	Network                  string `json:"network"`
	NetworkName              string `json:"network_name"`
	EscrowContract           string `json:"escrow_contract"`
	OperatorContract         string `json:"operator_contract"`
	TokenCollector           string `json:"token_collector"`
	TokenAddress             string `json:"token_address"`
	TokenSymbol              string `json:"token_symbol"`
	TokenDecimals            int    `json:"token_decimals"`
	ProviderAddress          string `json:"provider_address"`
	Amount                   string `json:"amount"`
	AmountBaseUnits          string `json:"amount_base_units"`
	DeliveryWindowSeconds    int64  `json:"delivery_window_seconds"`
	AutoReleaseWindowSeconds int64  `json:"auto_release_window_seconds"`
	ClaimLapseSeconds        int64  `json:"claim_lapse_seconds"`
	DeliveryDeadline         int64  `json:"delivery_deadline"`
	AutoReleaseAt            int64  `json:"auto_release_at"`
	AuthorizationExpiry      int64  `json:"authorization_expiry"`
	PaymentRequired          struct {
		X402Version int         `json:"x402Version"`
		Accepts     []X402Offer `json:"accepts"`
	} `json:"payment_required"`
	TypedData       EIP712TypedData `json:"typed_data"`
	ERC3009Nonce    string          `json:"erc3009_nonce"`
	PaymentEndpoint string          `json:"payment_endpoint"`
	FundingDeadline *string         `json:"funding_deadline"`
	ExplorerURL     string          `json:"explorer_url"`
	Note            string          `json:"note"`
	Raw             map[string]any  `json:"-"`
}

// X402PaymentPayload is an x402 v2 PaymentPayload: what is submitted.
type X402PaymentPayload struct {
	X402Version int       `json:"x402Version"`
	Accepted    X402Offer `json:"accepted"`
	Payload     struct {
		Authorization map[string]string `json:"authorization"`
		Signature     string            `json:"signature"`
		Salt          string            `json:"salt"`
	} `json:"payload"`
}

// X402PaymentResult is what the relay did. It is not whether the order is
// funded; read the order for that.
type X402PaymentResult struct {
	OrderID         string `json:"order_id"`
	OrderRef        string `json:"order_reference"`
	Status          string `json:"status"`
	PaymentInfoHash string `json:"payment_info_hash"`
	TransactionHash string `json:"transaction_hash"`
	BlockNumber     *int64 `json:"block_number"`
	ChainID         int64  `json:"chain_id"`
	Network         string `json:"network"`
	Payer           string `json:"payer"`
	AmountBaseUnits string `json:"amount_base_units"`
	Relayer         string `json:"relayer"`
	OrderStatus     string `json:"order_status"`
	ExplorerURL     string `json:"explorer_url"`
	Note            string `json:"note"`
}

// Relayed reports whether the authorization reached the chain, or was already there.
func (r X402PaymentResult) Relayed() bool {
	switch r.Status {
	case "collected", "submitted", "already_collected":
		return true
	}
	return false
}

// StillToVerify says what this result does not establish.
func (r X402PaymentResult) StillToVerify() string {
	if r.Status == "submitted" {
		return "The transaction was sent but not yet mined when the API answered. Watch the order: it " +
			"becomes funded when the chain confirms the PaymentHeld event. If the transaction fails, " +
			"the order stays pending and resubmitting the same payload is safe."
	}
	return "The chain has the hold. The order becomes funded when the indexer sees the event past the " +
		"confirmation depth; read the order rather than this result to know that. The receipt, once " +
		"issued, names the transaction and can be verified independently."
}

// ErrX402InstructionsInconsistent is returned when the instructions' typed data
// does not say what their plain fields say. Do not sign such a document.
var ErrX402InstructionsInconsistent = errors.New("agoreum: x402 instructions are inconsistent")

// ErrNotX402Rail is returned when the order pays on the direct escrow rail.
var ErrNotX402Rail = errors.New("agoreum: these payment instructions are for the direct escrow rail; fund the order from your wallet with createEscrow instead")

// X402Authorization is what the buyer is being asked to authorize, checked for
// internal consistency. Construct it with NewX402Authorization.
type X402Authorization struct {
	Instructions X402PaymentInstructions
}

// NewX402Authorization reads payment instructions and checks that the signable
// message agrees with the plain fields, field for field.
func NewX402Authorization(raw []byte) (X402Authorization, error) {
	var pi X402PaymentInstructions
	if err := json.Unmarshal(raw, &pi); err != nil {
		return X402Authorization{}, fmt.Errorf("agoreum: decoding payment instructions: %w", err)
	}
	_ = json.Unmarshal(raw, &pi.Raw)
	if pi.SettlementRail != "x402-auth-capture" {
		return X402Authorization{}, ErrNotX402Rail
	}
	auth := X402Authorization{Instructions: pi}
	if err := auth.CheckConsistency(); err != nil {
		return X402Authorization{}, err
	}
	return auth, nil
}

// CheckConsistency verifies the typed data and the offer against the plain fields.
func (a X402Authorization) CheckConsistency() error {
	i := a.Instructions
	var problems []string
	var offer X402Offer
	if len(i.PaymentRequired.Accepts) > 0 {
		offer = i.PaymentRequired.Accepts[0]
	}
	info, _ := offer.Extra["paymentInfo"].(map[string]any)
	msg := i.TypedData.Message
	if i.TypedData.Domain.ChainID != i.ChainID {
		problems = append(problems, "typed data chainId is not the instructions' chain")
	}
	if !strings.EqualFold(i.TypedData.Domain.VerifyingContract, i.TokenAddress) {
		problems = append(problems, "typed data verifyingContract is not the token")
	}
	if msg["value"] != i.AmountBaseUnits {
		problems = append(problems, "typed data value is not the order amount")
	}
	if !strings.EqualFold(msg["to"], i.TokenCollector) {
		problems = append(problems, "typed data recipient is not the token collector")
	}
	if msg["validAfter"] != "0" {
		problems = append(problems, "typed data validAfter is not zero")
	}
	if !strings.EqualFold(msg["nonce"], i.ERC3009Nonce) {
		problems = append(problems, "typed data nonce is not the published nonce")
	}
	if offer.Scheme != X402Scheme {
		problems = append(problems, "payment_required scheme is not auth-capture")
	}
	if offer.Network != i.Network {
		problems = append(problems, "payment_required network is not the instructions' network")
	}
	if offer.Amount != i.AmountBaseUnits {
		problems = append(problems, "payment_required amount is not the order amount")
	}
	if !strings.EqualFold(offer.PayTo, i.ProviderAddress) {
		problems = append(problems, "payment_required payTo is not the provider")
	}
	if !strings.EqualFold(anyString(info["operator"]), i.OperatorContract) {
		problems = append(problems, "paymentInfo operator is not the operator contract")
	}
	if anyInt(info["authorizationExpiry"]) != i.AuthorizationExpiry {
		problems = append(problems, "paymentInfo authorizationExpiry is not the published expiry")
	}
	if i.AuthorizationExpiry != i.AutoReleaseAt+i.ClaimLapseSeconds {
		problems = append(problems, "authorization_expiry is not auto_release_at plus the claim lapse")
	}
	if vb, ok := new(big.Int).SetString(msg["validBefore"], 10); ok && vb.Cmp(big.NewInt(i.AuthorizationExpiry)) > 0 {
		problems = append(problems, "typed data validBefore is after the authorization expiry")
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrX402InstructionsInconsistent, strings.Join(problems, "; "))
	}
	return nil
}

// Summary is one paragraph a person can read before signing.
func (a X402Authorization) Summary() string {
	i := a.Instructions
	whole := formatBaseUnits(i.AmountBaseUnits, i.TokenDecimals)
	return fmt.Sprintf(
		"Authorize %s %s on %s (%s) to be held by %s under operator %s for order %s, payable to %s. "+
			"Delivery is due by unix %d; anyone may release to the provider after %d; if nobody releases "+
			"or settles by %d you may reclaim it. The authorization must be collected before unix %s.",
		whole, i.TokenSymbol, i.NetworkName, i.Network, i.EscrowContract, i.OperatorContract, i.OrderRef,
		i.ProviderAddress, i.DeliveryDeadline, i.AutoReleaseAt, i.AuthorizationExpiry,
		i.TypedData.Message["validBefore"],
	)
}

// TypedDataFor returns the exact EIP-712 document to sign, with the payer filled in.
func (a X402Authorization) TypedDataFor(payer string) EIP712TypedData {
	doc := a.Instructions.TypedData
	msg := make(map[string]string, len(doc.Message)+1)
	for k, v := range doc.Message {
		msg[k] = v
	}
	msg["from"] = payer
	doc.Message = msg
	types := make(map[string][]EIP712Field, len(doc.Types))
	for k, v := range doc.Types {
		types[k] = append([]EIP712Field(nil), v...)
	}
	doc.Types = types
	return doc
}

// Sign signs the authorization with the caller's own signer and returns the
// payload to submit. The SDK never sees a key.
func (a X402Authorization) Sign(ctx context.Context, payer string, sign SignTypedData) (X402PaymentPayload, error) {
	var out X402PaymentPayload
	doc := a.TypedDataFor(payer)
	signature, err := sign(ctx, doc)
	if err != nil {
		return out, err
	}
	if !strings.HasPrefix(signature, "0x") || len(signature) < 132 {
		return out, errors.New("agoreum: the signer must return a 0x-prefixed hex signature of 65 bytes or more")
	}
	if len(a.Instructions.PaymentRequired.Accepts) == 0 {
		return out, errors.New("agoreum: the payment instructions carry no offer to accept")
	}
	offer := a.Instructions.PaymentRequired.Accepts[0]
	info, _ := offer.Extra["paymentInfo"].(map[string]any)
	out.X402Version = X402Version
	out.Accepted = offer
	out.Payload.Authorization = doc.Message
	out.Payload.Signature = signature
	out.Payload.Salt = anyString(info["salt"])
	return out, nil
}

// X402Authorization fetches what the buyer is asked to authorize for an order
// on the x402 rail. Read it, show Summary() to whoever decides, Sign() with
// your own signer and SubmitX402Payment the result. Returns ErrNotX402Rail for
// an order on the direct escrow rail and ErrX402InstructionsInconsistent if the
// signable message disagrees with the document's own plain fields.
func (o *Orders) X402Authorization(ctx context.Context, orderID string) (X402Authorization, error) {
	raw, err := o.client.request(ctx, http.MethodGet, "/orders/"+url.PathEscape(orderID)+"/payment-instructions", nil, nil)
	if err != nil {
		return X402Authorization{}, err
	}
	return NewX402Authorization(raw)
}

// SubmitX402Payment relays a signed authorization to the operator contract.
// Safe to call again with the same payload: a retry after a lost response
// answers "already_collected". The result says what the relay did, not whether
// the order is funded; the order says that once the chain confirms the hold.
// See X402PaymentResult.StillToVerify.
func (o *Orders) SubmitX402Payment(ctx context.Context, orderID string, payload X402PaymentPayload) (X402PaymentResult, error) {
	return doJSON[X402PaymentResult](ctx, o.client, http.MethodPost, "/orders/"+url.PathEscape(orderID)+"/payment", nil, payload)
}

// AuthorizeAndSubmitX402 does the three steps in one call, for callers that
// have decided already. Named for what it does: it authorizes and submits. It
// does not fund the order; the chain does, and the result's StillToVerify says
// how to see that.
func (o *Orders) AuthorizeAndSubmitX402(ctx context.Context, orderID, payer string, sign SignTypedData) (X402PaymentResult, error) {
	auth, err := o.X402Authorization(ctx, orderID)
	if err != nil {
		return X402PaymentResult{}, err
	}
	payload, err := auth.Sign(ctx, payer, sign)
	if err != nil {
		return X402PaymentResult{}, err
	}
	return o.SubmitX402Payment(ctx, orderID, payload)
}

func anyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return big.NewFloat(t).Text('f', 0)
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

func anyInt(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	case string:
		n, ok := new(big.Int).SetString(t, 10)
		if !ok {
			return -1
		}
		return n.Int64()
	default:
		return -1
	}
}

func formatBaseUnits(base string, decimals int) string {
	n, ok := new(big.Int).SetString(base, 10)
	if !ok {
		return base
	}
	s := n.String()
	if decimals <= 0 {
		return s
	}
	for len(s) <= decimals {
		s = "0" + s
	}
	return s[:len(s)-decimals] + "." + s[len(s)-decimals:]
}
