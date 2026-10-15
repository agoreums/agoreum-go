package agoreum

// From a wallet to an API key: the message is checked before the signer sees
// it, the steps go out in order with the right credentials, and the sign-in
// session is ended whether or not the mint worked. The challenge is the shape
// production returned on 2026-10-15, with address, nonce and times replaced.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	walletAddress   = "0x0bd63110306B2f5552d0F9943d5f6C97f4363840"
	walletNonce     = "c0ffee00c0ffee00c0ffee00"
	walletSignature = "0x" + "abababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababab"
	walletMinted    = "ak_test_" + "minted"
)

type challengeOpts struct {
	host, address, nonce, uri, extra string
	chain                            int64
}

func walletChallenge(o challengeOpts) string {
	if o.host == "" {
		o.host = "agoreum.xyz"
	}
	if o.address == "" {
		o.address = walletAddress
	}
	if o.nonce == "" {
		o.nonce = walletNonce
	}
	if o.uri == "" {
		o.uri = "https://" + o.host
	}
	if o.chain == 0 {
		o.chain = 8453
	}
	return fmt.Sprintf("%s wants you to sign in with your Ethereum account:\n%s\n\n"+
		"Sign in to Agoreum. This request will not trigger a blockchain transaction or cost any gas.\n\n"+
		"URI: %s\nVersion: 1\nChain ID: %d\nNonce: %s\n"+
		"Issued At: 2026-10-15T01:18:05.176357+00:00\nExpiration Time: 2026-10-15T01:28:05.169194+00:00%s",
		o.host, o.address, o.uri, o.chain, o.nonce, o.extra)
}

type walletCall struct {
	path, auth string
	body       map[string]any
}

// walletServer answers the four steps. The challenge names the host the test
// passes, because the client is pointed at the test server's address.
func walletServer(t *testing.T, message func(host string) string, mintStatus int) (*httptest.Server, *[]walletCall) {
	t.Helper()
	var calls []walletCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		calls = append(calls, walletCall{path: r.URL.Path, auth: r.Header.Get("Authorization"), body: body})
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/nonce":
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"nonce": walletNonce, "message": message(r.Host)})
		case "/api/v1/auth/signin":
			_ = json.NewEncoder(w).Encode(map[string]any{"tokens": map[string]any{"access_token": "jwt-access", "refresh_token": "jwt-refresh"}})
		case "/api/v1/api-keys":
			w.WriteHeader(mintStatus)
			if mintStatus == 201 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "77777777-7777-7777-7777-777777777777", "name": "my-agent", "prefix": "ak_live_abcd",
					"scopes": []string{"marketplace:read", "orders:write"}, "expires_at": nil, "token": walletMinted,
				})
			} else {
				_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"too many keys"}}`))
			}
		case "/api/v1/auth/logout":
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestCreateAPIKeyWithWalletRunsEveryStepInOrder(t *testing.T) {
	srv, calls := walletServer(t, func(host string) string { return walletChallenge(challengeOpts{host: host, uri: "http://" + host}) }, 201)
	var seen []string
	key, err := CreateAPIKeyWithWallet(context.Background(), WalletKeyParams{
		Address: walletAddress,
		Sign: func(_ context.Context, m string) (string, error) {
			seen = append(seen, m)
			return walletSignature, nil
		},
		Name:    "my-agent",
		Scopes:  []string{"marketplace:read", "orders:write"},
		BaseURL: srv.URL + "/api/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, c := range *calls {
		paths = append(paths, c.path)
	}
	if strings.Join(paths, " ") != "/api/v1/auth/nonce /api/v1/auth/signin /api/v1/api-keys /api/v1/auth/logout" {
		t.Fatalf("steps: %v", paths)
	}
	c := *calls
	if c[0].body["address"] != walletAddress || c[0].body["chain_id"] != float64(8453) {
		t.Errorf("nonce body: %v", c[0].body)
	}
	if len(seen) != 1 || c[1].body["message"] != seen[0] || c[1].body["signature"] != walletSignature || c[1].body["nonce"] != walletNonce {
		t.Errorf("signin body: %v", c[1].body)
	}
	if c[2].auth != "Bearer jwt-access" || c[2].body["name"] != "my-agent" || len(c[2].body["scopes"].([]any)) != 2 {
		t.Errorf("mint: %q %v", c[2].auth, c[2].body)
	}
	if c[3].auth != "Bearer jwt-access" || c[3].body["refresh_token"] != "jwt-refresh" {
		t.Errorf("logout: %q %v", c[3].auth, c[3].body)
	}
	if key.Token != walletMinted || key.Prefix != "ak_live_abcd" || key.Address != walletAddress || len(key.Scopes) != 2 {
		t.Errorf("key: %+v", key)
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v %s", key, key, key, key), walletMinted) {
		t.Error("printing the key shows its secret")
	}
}

func TestCreateAPIKeyWithWalletEndsTheSessionWhenTheMintFails(t *testing.T) {
	srv, calls := walletServer(t, func(host string) string { return walletChallenge(challengeOpts{host: host, uri: "http://" + host}) }, 409)
	_, err := CreateAPIKeyWithWallet(context.Background(), WalletKeyParams{
		Address: walletAddress, Name: "my-agent", BaseURL: srv.URL + "/api/v1",
		Sign: func(context.Context, string) (string, error) { return walletSignature, nil },
	})
	if !IsConflict(err) {
		t.Fatalf("want a conflict, got %v", err)
	}
	if last := (*calls)[len(*calls)-1]; last.path != "/api/v1/auth/logout" {
		t.Fatalf("last step %s, want logout", last.path)
	}
}

func TestAMessageThatIsNotOurSignInNeverReachesTheSigner(t *testing.T) {
	cases := map[string]struct {
		message func(host string) string
		reason  string
	}{
		"another site": {func(string) string { return walletChallenge(challengeOpts{host: "evil.example"}) }, `signs in to "evil.example"`},
		"another URI":  {func(h string) string { return walletChallenge(challengeOpts{host: h, uri: "https://evil.example"}) }, "its URI"},
		"another address": {func(h string) string {
			return walletChallenge(challengeOpts{host: h, uri: "http://" + h, address: "0x" + strings.Repeat("11", 20)})
		}, "different address"},
		"another chain": {func(h string) string { return walletChallenge(challengeOpts{host: h, uri: "http://" + h, chain: 1}) }, "not for chain 8453"},
		"another nonce": {func(h string) string {
			return walletChallenge(challengeOpts{host: h, uri: "http://" + h, nonce: "another"})
		}, "nonce"},
		"resources": {func(h string) string {
			return walletChallenge(challengeOpts{host: h, uri: "http://" + h, extra: "\nResources:\n- https://agoreum.xyz/everything"})
		}, "resources"},
		"not a sign-in": {func(string) string { return "Please sign: transfer everything" }, "not a sign-in message"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, calls := walletServer(t, tc.message, 201)
			signed := false
			_, err := CreateAPIKeyWithWallet(context.Background(), WalletKeyParams{
				Address: walletAddress, Name: "my-agent", BaseURL: srv.URL + "/api/v1",
				Sign: func(context.Context, string) (string, error) { signed = true; return walletSignature, nil },
			})
			var rejected *WalletChallengeRejected
			if !errors.As(err, &rejected) || !strings.Contains(rejected.Reason, tc.reason) {
				t.Fatalf("want a rejection mentioning %q, got %v", tc.reason, err)
			}
			if signed || len(*calls) != 1 {
				t.Fatalf("signed=%v calls=%d", signed, len(*calls))
			}
		})
	}
}

func TestCheckChallengeAcceptsBothHostsAndALocalPortSplit(t *testing.T) {
	if err := CheckChallenge(walletChallenge(challengeOpts{}), strings.ToLower(walletAddress), walletNonce, 8453, ""); err != nil {
		t.Error(err)
	}
	if err := CheckChallenge(walletChallenge(challengeOpts{host: "www.agoreum.xyz"}), walletAddress, walletNonce, 8453, ""); err != nil {
		t.Error(err)
	}
	local := walletChallenge(challengeOpts{host: "localhost:3000", uri: "http://localhost:3000", chain: 84532})
	if err := CheckChallenge(local, walletAddress, walletNonce, 84532, "http://localhost:8000/api/v1"); err != nil {
		t.Error(err)
	}
	if err := CheckChallenge(walletChallenge(challengeOpts{}), walletAddress, walletNonce, 8453, "https://staging.agoreum.xyz/api/v1"); err == nil {
		t.Error("a message for agoreum.xyz passed for staging.agoreum.xyz")
	}
}
