package agoreum

// Releasing, refunding or disputing an escrow, from your own wallet.
//
// GET /orders/{id}/settlement-options lists every on-chain exit from an
// order's escrow for the party asking, whether it is open now and, for release
// and refund, the complete calldata. This file turns one of those into a
// transaction: it rebuilds the calldata from the escrow id (dispute adds the
// caller's reason), returns a *SettlementOptionsInconsistent if the API's
// version differs, and a *SettlementNotAvailable when the action is not open to
// you yet. A dispute's reason is written into a public, permanent event on
// chain. The SDK never signs or sends.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	// ReleaseSelector is release(bytes32).
	ReleaseSelector = "0x67d42a8b"
	// RefundSelector is refund(bytes32).
	RefundSelector = "0x7249fbb6"
	// DisputeSelector is dispute(bytes32,string).
	DisputeSelector = "0x84146b9d"
	// MaxDisputeReasonBytes caps the reason written on chain.
	MaxDisputeReasonBytes = 1000
)

var settlementSelectors = map[string]string{"release": ReleaseSelector, "refund": RefundSelector, "dispute": DisputeSelector}

// SettlementActionOption is one on-chain exit as the API describes it.
type SettlementActionOption struct {
	Action      string  `json:"action"`
	Available   bool    `json:"available"`
	Who         string  `json:"who"`
	Reason      *string `json:"reason"`
	AvailableAt *string `json:"available_at"`
	Function    string  `json:"function"`
	Selector    string  `json:"selector"`
	Arguments   []struct {
		Name  string  `json:"name"`
		Type  string  `json:"type"`
		Value *string `json:"value"`
	} `json:"arguments"`
	Calldata *string `json:"calldata"`
	Contract *string `json:"contract"`
}

// SettlementOptions is every on-chain exit from an order's escrow, for the party asking.
type SettlementOptions struct {
	OrderID        string                   `json:"order_id"`
	OrderReference string                   `json:"order_reference"`
	SettlementRail string                   `json:"settlement_rail"`
	ChainID        int64                    `json:"chain_id"`
	NetworkName    string                   `json:"network_name"`
	EscrowContract string                   `json:"escrow_contract"`
	EscrowID       string                   `json:"escrow_id"`
	OnchainStatus  string                   `json:"onchain_status"`
	ContractPaused bool                     `json:"contract_paused"`
	YourRoles      []string                 `json:"your_roles"`
	Actions        []SettlementActionOption `json:"actions"`
	ExplorerURL    string                   `json:"explorer_url"`
	Note           string                   `json:"note"`
}

// SettlementTransaction is the one call to send from your wallet.
type SettlementTransaction struct {
	Action  string `json:"action"`
	ChainID int64  `json:"chain_id"`
	To      string `json:"to"`
	Data    string `json:"data"`
	Value   string `json:"value"`
}

// SettlementOptionsInconsistent is returned when the options do not say what
// their own fields say. Nothing is sent.
type SettlementOptionsInconsistent struct{ Reason string }

func (e *SettlementOptionsInconsistent) Error() string {
	return "agoreum: settlement options are inconsistent: " + e.Reason
}

// SettlementNotAvailable is returned when the action is not open to you now.
// Reason is the API's explanation; AvailableAt is when it opens, if known.
type SettlementNotAvailable struct {
	Action      string
	Reason      string
	AvailableAt string
}

func (e *SettlementNotAvailable) Error() string {
	return "agoreum: " + e.Action + " is not available now: " + e.Reason
}

func settlementInconsistent(format string, args ...any) error {
	return &SettlementOptionsInconsistent{Reason: fmt.Sprintf(format, args...)}
}

func escrowWord(escrowID string) (string, error) {
	if !bytes32Pattern.MatchString(escrowID) {
		return "", settlementInconsistent("escrow_id is not 32 bytes: %q", escrowID)
	}
	return strings.ToLower(escrowID[2:]), nil
}

// EncodeRelease returns release(escrowId) calldata.
func EncodeRelease(escrowID string) (string, error) {
	w, err := escrowWord(escrowID)
	return ReleaseSelector + w, err
}

// EncodeRefund returns refund(escrowId) calldata.
func EncodeRefund(escrowID string) (string, error) {
	w, err := escrowWord(escrowID)
	return RefundSelector + w, err
}

// EncodeDispute returns dispute(escrowId, reason) calldata. The reason becomes
// public on chain.
func EncodeDispute(escrowID, reason string) (string, error) {
	if strings.TrimSpace(reason) == "" {
		return "", fmt.Errorf("agoreum: a dispute needs a reason: it is what the arbiter reads first")
	}
	raw := []byte(reason)
	if len(raw) > MaxDisputeReasonBytes {
		return "", fmt.Errorf("agoreum: keep the reason under %d bytes; put the detail in a dispute statement", MaxDisputeReasonBytes)
	}
	w, err := escrowWord(escrowID)
	if err != nil {
		return "", err
	}
	body := hex.EncodeToString(raw)
	if pad := (len(raw)+31)/32*64 - len(body); pad > 0 {
		body += strings.Repeat("0", pad)
	}
	return DisputeSelector + w + fmt.Sprintf("%064x", 0x40) + fmt.Sprintf("%064x", len(raw)) + body, nil
}

// TransactionFromOptions builds the one call that takes action (release,
// refund or dispute) and checks it against the options. reason is required
// for a dispute.
func TransactionFromOptions(options SettlementOptions, action, reason string) (SettlementTransaction, error) {
	selector, ok := settlementSelectors[action]
	if !ok {
		return SettlementTransaction{}, fmt.Errorf("agoreum: action must be release, refund or dispute, not %q", action)
	}
	if options.SettlementRail != "" && options.SettlementRail != "escrow" {
		return SettlementTransaction{}, fmt.Errorf("agoreum: this order settles on the %s rail; send the calldata in SettlementOptions from your own wallet. Orders.Settle covers the direct escrow", options.SettlementRail)
	}
	var offered *SettlementActionOption
	for i := range options.Actions {
		if options.Actions[i].Action == action {
			offered = &options.Actions[i]
			break
		}
	}
	if offered == nil {
		return SettlementTransaction{}, settlementInconsistent("the settlement options do not list %s", action)
	}
	if !offered.Available {
		e := &SettlementNotAvailable{Action: action, Reason: action + " is not available to you now"}
		if offered.Reason != nil {
			e.Reason = *offered.Reason
		}
		if offered.AvailableAt != nil {
			e.AvailableAt = *offered.AvailableAt
		}
		return SettlementTransaction{}, e
	}
	if strings.ToLower(offered.Selector) != selector {
		return SettlementTransaction{}, settlementInconsistent("the %s selector is not the escrow's own", action)
	}
	if !addressPattern.MatchString(options.EscrowContract) {
		return SettlementTransaction{}, settlementInconsistent("escrow_contract is not an address: %q", options.EscrowContract)
	}
	if offered.Contract != nil && *offered.Contract != "" && !strings.EqualFold(*offered.Contract, options.EscrowContract) {
		return SettlementTransaction{}, settlementInconsistent("the %s call is addressed to another contract", action)
	}
	for _, arg := range offered.Arguments {
		if arg.Name == "escrowId" && (arg.Value == nil || !strings.EqualFold(*arg.Value, options.EscrowID)) {
			return SettlementTransaction{}, settlementInconsistent("the %s call names another escrow", action)
		}
	}
	var data string
	var err error
	switch action {
	case "dispute":
		data, err = EncodeDispute(options.EscrowID, reason)
	case "release":
		data, err = EncodeRelease(options.EscrowID)
	default:
		data, err = EncodeRefund(options.EscrowID)
	}
	if err != nil {
		return SettlementTransaction{}, err
	}
	if action != "dispute" && offered.Calldata != nil && *offered.Calldata != "" && strings.ToLower(*offered.Calldata) != data {
		return SettlementTransaction{}, settlementInconsistent("the API's %s calldata does not match the escrow id", action)
	}
	return SettlementTransaction{Action: action, ChainID: options.ChainID, To: strings.ToLower(options.EscrowContract), Data: data, Value: "0"}, nil
}

// SettlementOptions returns every on-chain exit from this order's escrow for
// you: release, refund and dispute, whether each is open now and why not.
func (o *Orders) SettlementOptions(ctx context.Context, orderID string) (SettlementOptions, error) {
	var out SettlementOptions
	raw, err := o.client.request(ctx, http.MethodGet, "/orders/"+url.PathEscape(orderID)+"/settlement-options", nil, nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("agoreum: decoding settlement options: %w", err)
	}
	return out, nil
}

// Settle releases, refunds or disputes from your own wallet. Release pays the
// provider (the buyer accepting the work, or anyone once auto-release is due);
// refund returns everything to the buyer (the provider declining, or the buyer
// once the delivery deadline passed with nothing delivered); dispute freezes
// the escrow for the arbiter, and reason is required for it. send sends the
// call from your wallet, waits until it is mined and returns its hash.
func (o *Orders) Settle(ctx context.Context, orderID, action, reason string, send func(context.Context, SettlementTransaction) (string, error)) (SentTransaction, error) {
	options, err := o.SettlementOptions(ctx, orderID)
	if err != nil {
		return SentTransaction{}, err
	}
	tx, err := TransactionFromOptions(options, action, reason)
	if err != nil {
		return SentTransaction{}, err
	}
	hash, err := send(ctx, tx)
	if err != nil {
		return SentTransaction{}, fmt.Errorf("agoreum: sending the %s transaction: %w", action, err)
	}
	if !txHashPattern.MatchString(hash) {
		return SentTransaction{}, fmt.Errorf("agoreum: send must return the 0x-prefixed 32-byte transaction hash, got %q", hash)
	}
	return SentTransaction{Step: action, TransactionHash: hash}, nil
}
