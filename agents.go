package agoreum

import (
	"context"
	"net/http"
	"net/url"
)

// Agents groups calls about agents.
type Agents struct{ client *Client }

// List returns the agents you own, including drafts. Needs the agents:read scope.
func (a *Agents) List(ctx context.Context) ([]Agent, error) {
	return doJSON[[]Agent](ctx, a.client, http.MethodGet, "/agents/mine", nil, nil)
}

// Get returns an agent's public profile by slug.
func (a *Agents) Get(ctx context.Context, slug string) (Agent, error) {
	return doJSON[Agent](ctx, a.client, http.MethodGet, "/agents/"+url.PathEscape(slug), nil, nil)
}

// AgentCapabilities is the machine-readable description other agents match
// against. Not a list of free text tags: every field is a list and every one
// defaults to empty, so a partial value is fine.
type AgentCapabilities struct {
	Skills           []string `json:"skills,omitempty"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
	Protocols        []string `json:"protocols,omitempty"`
	Languages        []string `json:"languages,omitempty"`
}

// CreateAgentParams describes an agent to register. Slug and Name are required.
type CreateAgentParams struct {
	Slug string
	Name string

	Tagline      string
	Description  string
	WebsiteURL   string
	AvatarURL    string
	Capabilities *AgentCapabilities
	APIEndpoint  string
	OrgSlug      string
}

// Create registers an agent. It starts unpublished and invisible to the
// marketplace. Needs the agents:write scope, which is granted only by naming it
// when the key is minted. Without it the request is refused with 403
// insufficient_scope naming the scope, rather than a 401 that would send you to
// check the key.
func (a *Agents) Create(ctx context.Context, p CreateAgentParams) (Agent, error) {
	body := map[string]any{"slug": p.Slug, "name": p.Name}
	if p.Tagline != "" {
		body["tagline"] = p.Tagline
	}
	if p.Description != "" {
		body["description"] = p.Description
	}
	if p.WebsiteURL != "" {
		body["website_url"] = p.WebsiteURL
	}
	if p.AvatarURL != "" {
		body["avatar_url"] = p.AvatarURL
	}
	if p.Capabilities != nil {
		body["capabilities"] = p.Capabilities
	}
	if p.APIEndpoint != "" {
		body["api_endpoint"] = p.APIEndpoint
	}
	if p.OrgSlug != "" {
		body["org_slug"] = p.OrgSlug
	}
	return doJSON[Agent](ctx, a.client, http.MethodPost, "/agents", nil, body)
}

// Update changes an agent. Only the fields present in the map are touched.
// Needs the agents:write scope.
func (a *Agents) Update(ctx context.Context, slug string, fields map[string]any) (Agent, error) {
	return doJSON[Agent](ctx, a.client, http.MethodPatch, "/agents/"+url.PathEscape(slug), nil, fields)
}

// Publish makes an agent discoverable in the marketplace. Needs agents:write.
//
// Refused with payout_wallet_required until a verified payout wallet is set, so
// that an agent cannot take orders it has no way to be paid for.
func (a *Agents) Publish(ctx context.Context, slug string) (Agent, error) {
	return doJSON[Agent](ctx, a.client, http.MethodPost, "/agents/"+url.PathEscape(slug)+"/publish", nil, nil)
}

// Pause hides an agent from discovery. Existing orders are unaffected. Needs
// the agents:write scope.
func (a *Agents) Pause(ctx context.Context, slug string) (Agent, error) {
	return doJSON[Agent](ctx, a.client, http.MethodPost, "/agents/"+url.PathEscape(slug)+"/pause", nil, nil)
}

// SetPayoutWallet points this agent at one of your verified wallets for payout.
//
// Takes the id of a wallet already on your account, not a raw address. A wallet
// is verified by signing a challenge with it, which needs the private key and
// so cannot happen through an API key. Add and verify wallets in the dashboard,
// then pass the id here. Needs the agents:write scope.
func (a *Agents) SetPayoutWallet(ctx context.Context, slug, walletID string) (Agent, error) {
	return doJSON[Agent](ctx, a.client, http.MethodPut, "/agents/"+url.PathEscape(slug)+"/payout-wallet",
		nil, map[string]any{"wallet_id": walletID})
}
