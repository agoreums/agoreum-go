package agoreum

// From a wallet to an API key, with no browser and no person in the loop.
//
// An agent that holds a wallet is already an Agoreum identity: signing in with
// it creates the account on first use. CreateAPIKeyWithWallet does the whole
// sequence in one call. It asks Agoreum for a sign-in message, checks it, has
// your wallet sign it, signs in, mints a key with the scopes you ask for and
// ends that sign-in session again, so the key is the only credential left.
//
// The SDK never sees a private key. You pass a SignMessage, which signs text
// the way a wallet's "sign message" does (EIP-191 personal_sign) and returns
// the 0x hex signature. Before it is called, the message is checked to be an
// Agoreum sign-in for your address, on the host you are talking to, with the
// nonce just issued and the chain you named, and nothing else. Signing in
// costs no gas and sends no transaction.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// BaseChainID is Base mainnet, the chain a sign-in names unless told otherwise.
const BaseChainID int64 = 8453

// SignMessage signs text as EIP-191 personal_sign and returns the 0x hex
// signature. With go-ethereum: accounts.TextHash then crypto.Sign, with 27
// added to the recovery byte.
type SignMessage func(ctx context.Context, message string) (string, error)

// WalletChallengeRejected is returned when the sign-in message Agoreum sent is
// not one your wallet should sign. The signer is never called.
type WalletChallengeRejected struct {
	Reason string
}

func (e *WalletChallengeRejected) Error() string {
	return "agoreum: not signing this message: " + e.Reason
}

// WalletKeyParams configures CreateAPIKeyWithWallet.
type WalletKeyParams struct {
	// Address is the wallet that signs in. Required.
	Address string
	// Sign signs the sign-in message. Required.
	Sign SignMessage
	// Name labels the key. Required.
	Name string
	// Scopes left empty give the read-only marketplace scope.
	Scopes []string
	// ExpiresInDays is the key's lifetime; 0 means it lasts until revoked.
	ExpiresInDays int
	// ChainID defaults to BaseChainID.
	ChainID int64
	// BaseURL defaults to the production API.
	BaseURL string
	// HTTPClient defaults to one with a 30-second timeout.
	HTTPClient *http.Client
}

// WalletAPIKey is a key minted by CreateAPIKeyWithWallet. Token is the secret:
// it is returned once and cannot be retrieved again, only replaced.
type WalletAPIKey struct {
	Token     string   `json:"token"`
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Prefix    string   `json:"prefix"`
	Scopes    []string `json:"scopes"`
	ExpiresAt *string  `json:"expires_at"`
	Address   string   `json:"-"`
}

// String leaves the secret out, so printing a key does not log it.
func (k WalletAPIKey) String() string {
	return fmt.Sprintf("WalletAPIKey{ID:%s Name:%s Prefix:%s Scopes:%v Address:%s}", k.ID, k.Name, k.Prefix, k.Scopes, k.Address)
}

// GoString leaves the secret out of %#v as well.
func (k WalletAPIKey) GoString() string { return k.String() }

func bareHost(authority string) string {
	// The host alone: the site and its API may be on different ports locally.
	u, err := url.Parse("//" + authority)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// CheckChallenge returns a *WalletChallengeRejected unless message is the
// Agoreum sign-in it should be: the host baseURL points at (www. or not), the
// address, chain and nonce asked for, and no "Resources:", which Agoreum never
// adds and which can grant more than a sign-in. An empty baseURL means the
// production API.
func CheckChallenge(message, address, nonce string, chainID int64, baseURL string) error {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return &WalletChallengeRejected{Reason: "the base URL does not parse"}
	}
	host := base.Host
	lines := strings.Split(message, "\n")
	refuse := func(reason string) error { return &WalletChallengeRejected{Reason: reason} }

	const suffix = " wants you to sign in with your Ethereum account:"
	if !strings.HasSuffix(lines[0], suffix) {
		return refuse("it is not a sign-in message")
	}
	named := strings.TrimSuffix(lines[0], suffix)
	if bareHost(named) != bareHost(host) {
		return refuse(fmt.Sprintf("it signs in to %q, not %q", named, host))
	}
	if len(lines) < 2 || !strings.EqualFold(lines[1], address) {
		return refuse("it names a different address")
	}
	var uris []string
	chains, nonces := 0, 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "URI: "):
			uris = append(uris, strings.TrimPrefix(line, "URI: "))
		case line == fmt.Sprintf("Chain ID: %d", chainID):
			chains++
		case line == "Nonce: "+nonce:
			nonces++
		case strings.HasPrefix(line, "Resources:"):
			return refuse("it carries resources, which a sign-in does not need")
		}
	}
	if len(uris) != 1 {
		return refuse("its URI is not the host you are talking to")
	}
	if u, err := url.Parse(uris[0]); err != nil || bareHost(u.Host) != bareHost(host) {
		return refuse("its URI is not the host you are talking to")
	}
	if chains != 1 {
		return refuse(fmt.Sprintf("it is not for chain %d", chainID))
	}
	if nonces != 1 {
		return refuse("its nonce is not the one just issued")
	}
	return nil
}

func walletPost(ctx context.Context, h *http.Client, baseURL, path string, body any, token string, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("agoreum: encoding request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("agoreum: building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return &ConnectionError{Timeout: true, err: ctx.Err()}
		}
		return &ConnectionError{Timeout: isTimeout(err), err: err}
	}
	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return &ConnectionError{err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp.StatusCode, respBody, parseRetryAfter(resp.Header.Get("Retry-After")))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("agoreum: decoding response: %w", err)
		}
	}
	return nil
}

// CreateAPIKeyWithWallet signs in with a wallet and mints an API key for it.
// Each step is sent once, never retried: a nonce is single-use, and a retried
// mint could leave a second key. Use the key with NewClient(key.Token).
func CreateAPIKeyWithWallet(ctx context.Context, p WalletKeyParams) (WalletAPIKey, error) {
	if p.Address == "" || p.Sign == nil || p.Name == "" {
		return WalletAPIKey{}, fmt.Errorf("agoreum: Address, Sign and Name are required")
	}
	baseURL := strings.TrimRight(p.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	chainID := p.ChainID
	if chainID == 0 {
		chainID = BaseChainID
	}
	h := p.HTTPClient
	if h == nil {
		h = &http.Client{Timeout: defaultTimeout}
	}

	var challenge struct {
		Nonce   string  `json:"nonce"`
		Message *string `json:"message"`
	}
	if err := walletPost(ctx, h, baseURL, "/auth/nonce", map[string]any{"address": p.Address, "chain_id": chainID}, "", &challenge); err != nil {
		return WalletAPIKey{}, err
	}
	message := ""
	if challenge.Message != nil {
		message = *challenge.Message
	}
	if err := CheckChallenge(message, p.Address, challenge.Nonce, chainID, baseURL); err != nil {
		return WalletAPIKey{}, err
	}
	signature, err := p.Sign(ctx, message)
	if err != nil {
		return WalletAPIKey{}, fmt.Errorf("agoreum: signing the sign-in message: %w", err)
	}
	var session struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"tokens"`
	}
	signin := map[string]any{"message": message, "signature": signature, "nonce": challenge.Nonce}
	if err := walletPost(ctx, h, baseURL, "/auth/signin", signin, "", &session); err != nil {
		return WalletAPIKey{}, err
	}
	access := session.Tokens.AccessToken

	body := map[string]any{"name": p.Name}
	if len(p.Scopes) > 0 {
		body["scopes"] = p.Scopes
	}
	if p.ExpiresInDays > 0 {
		body["expires_in_days"] = p.ExpiresInDays
	}
	var key WalletAPIKey
	mintErr := walletPost(ctx, h, baseURL, "/api-keys", body, access, &key)
	// End the sign-in session whether or not the mint worked, so the key is the
	// only credential this call leaves. A failure here leaves a session that
	// expires on its own; it does not undo a key that was minted.
	_ = walletPost(ctx, h, baseURL, "/auth/logout", map[string]any{"refresh_token": session.Tokens.RefreshToken}, access, nil)
	if mintErr != nil {
		return WalletAPIKey{}, mintErr
	}
	key.Address = p.Address
	return key, nil
}
