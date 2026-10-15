package agoreum

// Funding an order on the direct escrow rail, from your own wallet.
//
// The payment instructions describe two calls: an ERC-20 approve letting the
// escrow contract take the order's amount, then createEscrow. This file builds
// both from the instructions' own plain fields with no ABI library, because
// every argument is a static type and so one 32-byte word. When the API also
// sent its encoded transactions (API 0.33.0 and later), they must match these
// bytes exactly, or a *FundingInstructionsInconsistent is returned and nothing
// is sent. The SDK never signs or sends: Orders.Fund hands each call to your
// SendTransaction, which sends it, waits until it is mined and returns its hash.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	// ApproveSelector is approve(address,uint256).
	ApproveSelector = "0x095ea7b3"
	// CreateEscrowSelector is createEscrow(bytes32,address,address,uint256,uint64,uint64).
	CreateEscrowSelector = "0xae6d5d16"
)

// FundingTransaction is one call to send from the paying wallet.
type FundingTransaction struct {
	Step    string `json:"step"`
	ChainID int64  `json:"chain_id"`
	To      string `json:"to"`
	Data    string `json:"data"`
	Value   string `json:"value"`
}

// SentTransaction records one call Fund sent.
type SentTransaction struct {
	Step            string `json:"step"`
	TransactionHash string `json:"transaction_hash"`
}

// SendTransaction sends one call from the paying wallet, waits until it is
// mined, and returns its 0x-prefixed hash.
type SendTransaction func(ctx context.Context, tx FundingTransaction) (string, error)

// FundingInstructionsInconsistent is returned when the instructions' encoded
// calls do not say what their plain fields say. Nothing is sent.
type FundingInstructionsInconsistent struct {
	Reason string
}

func (e *FundingInstructionsInconsistent) Error() string {
	return "agoreum: funding instructions are inconsistent: " + e.Reason
}

var (
	addressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	bytes32Pattern = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
	txHashPattern  = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
)

func inconsistent(format string, args ...any) error {
	return &FundingInstructionsInconsistent{Reason: fmt.Sprintf(format, args...)}
}

func addressWord(value, name string) (string, error) {
	if !addressPattern.MatchString(value) {
		return "", inconsistent("%s is not an address: %q", name, value)
	}
	return strings.Repeat("0", 24) + strings.ToLower(value[2:]), nil
}

func uintWord(value, name string, bits uint) (string, error) {
	n, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return "", inconsistent("%s is not an integer: %q", name, value)
	}
	if n.Sign() < 0 || n.BitLen() > int(bits) {
		return "", inconsistent("%s does not fit uint%d: %q", name, bits, value)
	}
	return fmt.Sprintf("%064x", n), nil
}

// EncodeApprove returns approve(spender, amount) calldata.
func EncodeApprove(spender, amountBaseUnits string) (string, error) {
	s, err := addressWord(spender, "escrow_contract")
	if err != nil {
		return "", err
	}
	a, err := uintWord(amountBaseUnits, "amount_base_units", 256)
	if err != nil {
		return "", err
	}
	return ApproveSelector + s + a, nil
}

// EncodeCreateEscrow returns createEscrow(escrowId, provider, token, amount,
// deliveryWindow, autoReleaseWindow) calldata.
func EncodeCreateEscrow(escrowID, provider, token, amountBaseUnits, deliveryWindowSeconds, autoReleaseWindowSeconds string) (string, error) {
	if !bytes32Pattern.MatchString(escrowID) {
		return "", inconsistent("escrow_id is not 32 bytes: %q", escrowID)
	}
	words := []string{strings.ToLower(escrowID[2:])}
	for _, part := range []struct {
		value, name string
		address     bool
		bits        uint
	}{
		{provider, "provider_address", true, 0},
		{token, "token_address", true, 0},
		{amountBaseUnits, "amount_base_units", false, 256},
		{deliveryWindowSeconds, "delivery_window_seconds", false, 64},
		{autoReleaseWindowSeconds, "auto_release_window_seconds", false, 64},
	} {
		var word string
		var err error
		if part.address {
			word, err = addressWord(part.value, part.name)
		} else {
			word, err = uintWord(part.value, part.name, part.bits)
		}
		if err != nil {
			return "", err
		}
		words = append(words, word)
	}
	return CreateEscrowSelector + strings.Join(words, ""), nil
}

type fundingInstructions struct {
	SettlementRail          string      `json:"settlement_rail"`
	ChainID                 int64       `json:"chain_id"`
	EscrowContract          string      `json:"escrow_contract"`
	TokenAddress            string      `json:"token_address"`
	EscrowID                string      `json:"escrow_id"`
	ProviderAddress         string      `json:"provider_address"`
	AmountBaseUnits         string      `json:"amount_base_units"`
	DeliveryWindowSeconds   json.Number `json:"delivery_window_seconds"`
	AutoReleaseWindowSecond json.Number `json:"auto_release_window_seconds"`
	CreateEscrowSelector    string      `json:"create_escrow_selector"`
	Transactions            []struct {
		Step  string `json:"step"`
		To    string `json:"to"`
		Data  string `json:"data"`
		Value string `json:"value"`
	} `json:"transactions"`
}

// TransactionsFromInstructions builds the two calls to send, in order, from a
// payment-instructions response body, and checks them against the API's own
// encoding when present.
func TransactionsFromInstructions(body []byte) ([]FundingTransaction, error) {
	var in fundingInstructions
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("agoreum: decoding payment instructions: %w", err)
	}
	if in.SettlementRail != "" && in.SettlementRail != "escrow" {
		return nil, fmt.Errorf("agoreum: this order settles on the %s rail, which is funded by a signature, not by transactions: use Orders.AuthorizeAndSubmitX402", in.SettlementRail)
	}
	if in.CreateEscrowSelector != "" && strings.ToLower(in.CreateEscrowSelector) != CreateEscrowSelector {
		return nil, inconsistent("create_escrow_selector is not createEscrow(bytes32,address,address,uint256,uint64,uint64)")
	}
	approve, err := EncodeApprove(in.EscrowContract, in.AmountBaseUnits)
	if err != nil {
		return nil, err
	}
	create, err := EncodeCreateEscrow(in.EscrowID, in.ProviderAddress, in.TokenAddress, in.AmountBaseUnits,
		in.DeliveryWindowSeconds.String(), in.AutoReleaseWindowSecond.String())
	if err != nil {
		return nil, err
	}
	built := []FundingTransaction{
		{Step: "approve", ChainID: in.ChainID, To: strings.ToLower(in.TokenAddress), Data: approve, Value: "0"},
		{Step: "create_escrow", ChainID: in.ChainID, To: strings.ToLower(in.EscrowContract), Data: create, Value: "0"},
	}
	if len(in.Transactions) > 0 {
		if len(in.Transactions) != len(built) {
			return nil, inconsistent("expected 2 transactions, the API sent %d", len(in.Transactions))
		}
		for i, theirs := range in.Transactions {
			mine := built[i]
			value := theirs.Value
			if value == "" {
				value = "0"
			}
			if theirs.Step != mine.Step || strings.ToLower(theirs.To) != mine.To ||
				strings.ToLower(theirs.Data) != mine.Data || (value != "0" && value != "0x0") {
				return nil, inconsistent("the API's %s transaction does not match the fields beside it", mine.Step)
			}
		}
	}
	return built, nil
}

// FundingTransactions returns the approve and createEscrow calls that fund
// this order, in order, built here from the instructions' own fields and
// checked against the API's encoding. Send the second only after the first is
// mined, or let Fund do that with your own sender.
func (o *Orders) FundingTransactions(ctx context.Context, orderID string) ([]FundingTransaction, error) {
	raw, err := o.client.request(ctx, http.MethodGet, "/orders/"+url.PathEscape(orderID)+"/payment-instructions", nil, nil)
	if err != nil {
		return nil, err
	}
	return TransactionsFromInstructions(raw)
}

// Fund funds an escrow-rail order with your own wallet: approve, then
// createEscrow. send sends each call from the paying wallet, waits until it is
// mined and returns its hash; the SDK never sees a key. The order shows as
// funded once Agoreum's indexer sees the escrow on chain.
func (o *Orders) Fund(ctx context.Context, orderID string, send SendTransaction) ([]SentTransaction, error) {
	txs, err := o.FundingTransactions(ctx, orderID)
	if err != nil {
		return nil, err
	}
	var sent []SentTransaction
	for _, tx := range txs {
		hash, err := send(ctx, tx)
		if err != nil {
			return sent, fmt.Errorf("agoreum: sending the %s transaction: %w", tx.Step, err)
		}
		if !txHashPattern.MatchString(hash) {
			return sent, fmt.Errorf("agoreum: send must return the 0x-prefixed 32-byte transaction hash, got %q", hash)
		}
		sent = append(sent, SentTransaction{Step: tx.Step, TransactionHash: hash})
	}
	return sent, nil
}
