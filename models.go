package agoreum

import "time"

// Me is the identity behind the calling API key (GET /me).
type Me struct {
	ID             string         `json:"id"`
	Username       *string        `json:"username"`
	DisplayName    *string        `json:"display_name"`
	PrimaryAddress string         `json:"primary_address"`
	Role           string         `json:"role"`
	CreatedAt      time.Time      `json:"created_at"`
	Auth           map[string]any `json:"auth"`
}

// Scopes returns the scopes granted to the calling API key, if the API reported them.
func (m Me) Scopes() []string {
	raw, ok := m.Auth["scopes"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if str, ok := s.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

// ServiceAgentSummary is the agent a service belongs to, as embedded in listings.
type ServiceAgentSummary struct {
	ID               string  `json:"id"`
	Slug             string  `json:"slug"`
	Name             string  `json:"name"`
	VerificationTier *string `json:"verification_tier"`
}

// Service is a marketplace service listing. Price is a decimal string (never a
// float, so precision is never lost); it is nil for negotiated pricing.
type Service struct {
	ID                  string              `json:"id"`
	Slug                string              `json:"slug"`
	Title               string              `json:"title"`
	Summary             *string             `json:"summary"`
	PricingModel        string              `json:"pricing_model"`
	Price               *string             `json:"price"`
	PriceCurrency       string              `json:"price_currency"`
	PriceUnit           *string             `json:"price_unit"`
	DeliveryTimeHours   *int                `json:"delivery_time_hours"`
	Tags                []string            `json:"tags"`
	CompletedOrderCount int                 `json:"completed_order_count"`
	ReviewCount         int                 `json:"review_count"`
	AverageRating       *float64            `json:"average_rating"`
	Agent               ServiceAgentSummary `json:"agent"`
}

// Agent is an agent from the public directory (or one of your own).
type Agent struct {
	ID                    string   `json:"id"`
	Slug                  string   `json:"slug"`
	Name                  string   `json:"name"`
	Tagline               *string  `json:"tagline"`
	AvatarURL             *string  `json:"avatar_url"`
	VerificationTier      *string  `json:"verification_tier"`
	VerifiedDomain        *string  `json:"verified_domain"`
	CompletedOrders       int      `json:"completed_orders"`
	ReviewCount           int      `json:"review_count"`
	AverageRating         *float64 `json:"average_rating"`
	PublishedServiceCount int      `json:"published_service_count"`
}

// Order is an order you placed or received. GET /orders/{id} returns the same shape
// with extra detail (requirements, escrow, transactions); read those from RawJSON if
// you need them.
type Order struct {
	ID              string     `json:"id"`
	Reference       string     `json:"reference"`
	Status          string     `json:"status"`
	Quantity        int        `json:"quantity"`
	UnitPrice       string     `json:"unit_price"`
	Subtotal        string     `json:"subtotal"`
	PlatformFee     string     `json:"platform_fee"`
	TotalAmount     string     `json:"total_amount"`
	Currency        string     `json:"currency"`
	PlatformFeeBps  int        `json:"platform_fee_bps"`
	CreatedAt       time.Time  `json:"created_at"`
	FundingDeadline *time.Time `json:"funding_deadline"`
	FundedAt        *time.Time `json:"funded_at"`
	DeliveredAt     *time.Time `json:"delivered_at"`
	AutoReleaseAt   *time.Time `json:"auto_release_at"`
	CompletedAt     *time.Time `json:"completed_at"`
}

// Page is one page of a search result: the items plus the true total and window.
type Page[T any] struct {
	Items  []T     `json:"items"`
	Total  int     `json:"total"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
	Query  *string `json:"query"`
	Sort   *string `json:"sort"`
}

// HasMore reports whether there are more results beyond this page.
func (p Page[T]) HasMore() bool {
	return p.Offset+len(p.Items) < p.Total
}

// PaymentInstructions is everything a wallet needs to fund an order itself. The
// platform never holds funds or signs, it describes the transaction; the buyer's
// own wallet builds, signs, and broadcasts it.
type PaymentInstructions struct {
	OrderID       string `json:"order_id"`
	OrderRef      string `json:"order_reference"`
	ChainID       int    `json:"chain_id"`
	NetworkName   string `json:"network_name"`
	EscrowAddress string `json:"escrow_contract"`
	TokenAddress  string `json:"token_address"`
	TokenSymbol   string `json:"token_symbol"`
	TokenDecimals int    `json:"token_decimals"`
	EscrowID      string `json:"escrow_id"`
	Provider      string `json:"provider_address"`
	// Raw preserves the full payload, including the exact base-unit amount fields.
	Raw map[string]any `json:"-"`
}
