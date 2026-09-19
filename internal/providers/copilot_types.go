package providers

import "encoding/json"

// CopilotTarget specifies how GitHub Copilot usage and billing metrics should be queried.
type CopilotTarget struct {
	Mode      string // "oauth" (default) or "wsl"
	WslDistro string // optional WSL distro name
}

// CopilotUsage mirrors https://api.github.com/copilot_internal/user's response
// format for quota and plan information, along with optional GitHub Actions
// and Codespaces billing metrics.
type CopilotUsage struct {
	CopilotPlan    string                          `json:"copilot_plan"`
	QuotaResetDate string                          `json:"quota_reset_date"`
	QuotaSnapshots map[string]CopilotQuotaSnapshot `json:"quota_snapshots"`
	FetchedAt      string                          `json:"fetchedAt"`
	User           string                          `json:"user,omitempty"`

	// Additional billing metrics for WSL GH CLI mode
	ActionsMinutesUsed      float64 `json:"actions_minutes_used,omitempty"`
	ActionsIncludedMinutes  float64 `json:"actions_included_minutes,omitempty"`
	HasActionsBilling       bool    `json:"has_actions_billing,omitempty"`
	CodespacesHoursUsed     float64 `json:"codespaces_hours_used,omitempty"`
	CodespacesIncludedHours float64 `json:"codespaces_included_hours,omitempty"`
	HasCodespacesBilling    bool    `json:"has_codespaces_billing,omitempty"`

	// Raw contains the untrimmed response body ParseCopilotUsage received.
	Raw json.RawMessage `json:"-"`

	DiagnosisFields
}

// GitHubBillingUsageItem mirrors an item from /users/{username}/settings/billing/usage/summary
type GitHubBillingUsageItem struct {
	Product       string  `json:"product"`
	SKU           string  `json:"sku"`
	UnitType      string  `json:"unitType"`
	GrossQuantity float64 `json:"grossQuantity"`
	NetQuantity   float64 `json:"netQuantity"`
}

// GitHubBillingUsageSummary mirrors /users/{username}/settings/billing/usage/summary
type GitHubBillingUsageSummary struct {
	User       string                   `json:"user"`
	UsageItems []GitHubBillingUsageItem `json:"usageItems"`
}

// CopilotQuotaSnapshot represents the quota consumption state for an individual
// feature (e.g. premium_interactions, chat, completions).
type CopilotQuotaSnapshot struct {
	Entitlement      float64 `json:"entitlement"`
	Remaining        float64 `json:"remaining"`
	PercentRemaining float64 `json:"percent_remaining"`
	Unlimited        bool    `json:"unlimited"`
}

// copilotAuth is the stored credential shape for GitHub Copilot.
type copilotAuth struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
	} `json:"tokens"`
}
