package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/jmnote/aigauge/internal/auth"
)

func TestParseCopilotUsage(t *testing.T) {
	data := readFixture(t, "copilot-usage.json")
	usage, err := ParseCopilotUsage(data)
	if err != nil {
		t.Fatalf("ParseCopilotUsage() error = %v", err)
	}
	if usage.CopilotPlan != "individual" {
		t.Errorf("CopilotPlan = %q, want %q", usage.CopilotPlan, "individual")
	}
	if usage.QuotaResetDate != "2026-10-01T00:00:00Z" {
		t.Errorf("QuotaResetDate = %q, want %q", usage.QuotaResetDate, "2026-10-01T00:00:00Z")
	}
	prem, ok := usage.QuotaSnapshots["premium_interactions"]
	if !ok {
		t.Fatalf("missing premium_interactions snapshot")
	}
	if prem.PercentRemaining != 75.0 || prem.Remaining != 375 {
		t.Errorf("premium_interactions = %+v, want 75.0%% and 375 remaining", prem)
	}
}

func TestCopilotToDisplay(t *testing.T) {
	data := readFixture(t, "copilot-usage.json")
	usage, err := ParseCopilotUsage(data)
	if err != nil {
		t.Fatalf("ParseCopilotUsage() error = %v", err)
	}
	usage.Status = StatusConnected
	usage.FetchedAt = "2026-09-19T12:00:00Z"

	display := usage.ToDisplay()
	if display.Status != StatusConnected {
		t.Fatalf("display.Status = %q, want %q", display.Status, StatusConnected)
	}
	if display.Plan != "individual" {
		t.Errorf("display.Plan = %q, want %q", display.Plan, "individual")
	}
	if len(display.Groups) != 1 {
		t.Fatalf("display.Groups count = %d, want 1", len(display.Groups))
	}
	buckets := display.Groups[0].Buckets
	// First bucket should be labeled monthly with detail formatted with remaining / entitlement
	if buckets[0].Label != "monthly" || buckets[0].Detail != "375/500" || buckets[0].Remaining != 75.0 {
		t.Errorf("bucket[0] = %+v, want monthly with 375/500 and 75%%", buckets[0])
	}
	if buckets[0].ResetTime != "2026-10-01T00:00:00Z" {
		t.Errorf("bucket[0].ResetTime = %q, want %q", buckets[0].ResetTime, "2026-10-01T00:00:00Z")
	}

	// Chat and Completions should be omitted, leaving only Copilot
	if len(buckets) != 1 {
		t.Fatalf("expected only 1 bucket, got %d", len(buckets))
	}
}

func TestCopilotToDisplayFallsBackToZeroWithoutPremiumSnapshot(t *testing.T) {
	data := readFixture(t, "copilot-usage-no-premium.json")
	usage, err := ParseCopilotUsage(data)
	if err != nil {
		t.Fatalf("ParseCopilotUsage() error = %v", err)
	}
	usage.Status = StatusConnected
	usage.FetchedAt = "2026-09-19T12:00:00Z"

	display := usage.ToDisplay()
	if display.Status != StatusConnected {
		t.Fatalf("display.Status = %q, want %q", display.Status, StatusConnected)
	}
	if len(display.Groups) != 1 || len(display.Groups[0].Buckets) != 1 {
		t.Fatalf("display.Groups = %+v, want 1 group with 1 bucket", display.Groups)
	}
	bucket := display.Groups[0].Buckets[0]
	if bucket.Label != "monthly" || bucket.Detail != "" || bucket.Remaining != 0 {
		t.Errorf("bucket = %+v, want monthly at 0%% with no detail", bucket)
	}
}

func TestDiagnoseCopilot(t *testing.T) {
	ctx := context.Background()

	// 1. No token -> login required
	depsNoToken := providerDeps{
		getToken: func(string) (*auth.Token, error) {
			return nil, nil
		},
	}
	diag, _, ok := diagnoseCopilot(ctx, depsNoToken, "test-instance", false)
	if ok || diag.Status != StatusLoginRequired {
		t.Errorf("diag without token = %+v, want StatusLoginRequired and not ok", diag)
	}

	// 2. Token present, inactive -> auth check required
	depsWithToken := providerDeps{
		getToken: func(string) (*auth.Token, error) {
			return &auth.Token{AccessToken: "gho_sample_token"}, nil
		},
	}
	diag, creds, ok := diagnoseCopilot(ctx, depsWithToken, "test-instance", false)
	if ok || diag.Status != StatusAuthCheckRequired {
		t.Errorf("diag with token inactive = %+v, want StatusAuthCheckRequired and not ok", diag)
	}
	if creds.Tokens.AccessToken != "gho_sample_token" {
		t.Errorf("creds token = %q, want %q", creds.Tokens.AccessToken, "gho_sample_token")
	}

	// 3. Token present, active -> connected ok
	diagActive, _, okActive := diagnoseCopilot(ctx, depsWithToken, "test-instance", true)
	if !okActive || diagActive.Status != "" {
		t.Errorf("diag with token active = %+v (ok=%v), want ok=true", diagActive, okActive)
	}
}

func TestCopilotToDisplayWSLWithBilling(t *testing.T) {
	data := readFixture(t, "copilot-usage.json")
	usage, err := ParseCopilotUsage(data)
	if err != nil {
		t.Fatalf("ParseCopilotUsage() error = %v", err)
	}
	usage.Status = StatusConnected
	usage.FetchedAt = "2026-09-20T12:00:00Z"
	usage.HasActionsBilling = true
	usage.ActionsIncludedMinutes = 2000
	usage.ActionsMinutesUsed = 428
	usage.HasCodespacesBilling = true
	usage.CodespacesHoursUsed = 3.5

	display := usage.ToDisplay()
	if display.Status != StatusConnected {
		t.Fatalf("display.Status = %q, want %q", display.Status, StatusConnected)
	}
	if len(display.Groups) != 3 {
		t.Fatalf("display.Groups count = %d, want 3", len(display.Groups))
	}

	// Group 1: Copilot
	if display.Groups[0].Name != "Copilot" || len(display.Groups[0].Buckets) != 1 {
		t.Fatalf("group[0] = %+v, want Copilot", display.Groups[0])
	}
	b0 := display.Groups[0].Buckets[0]
	if b0.Label != "monthly" || b0.Detail != "375/500" || b0.Remaining != 75.0 {
		t.Errorf("b0 = %+v, want monthly with 375/500 and 75%%", b0)
	}

	// Group 2: GitHub Actions
	if display.Groups[1].Name != "GitHub Actions" || len(display.Groups[1].Buckets) != 1 {
		t.Fatalf("group[1] = %+v, want GitHub Actions", display.Groups[1])
	}
	b1 := display.Groups[1].Buckets[0]
	if b1.Label != "monthly" || b1.Detail != "1572/2000m" || b1.Remaining != 78.6 {
		t.Errorf("b1 = %+v, want monthly with 1572/2000m and 78.6%%", b1)
	}

	// Group 3: Codespace
	if display.Groups[2].Name != "Codespace" || len(display.Groups[2].Buckets) != 1 {
		t.Fatalf("group[2] = %+v, want Codespace", display.Groups[2])
	}
	b2 := display.Groups[2].Buckets[0]
	if b2.Label != "monthly" || b2.Detail != "116/120h" || b2.Remaining != 97.1 {
		t.Errorf("b2 = %+v, want monthly with 116/120h and 97.1%%", b2)
	}
}

func TestBuildGhCommand(t *testing.T) {
	targetOAuth := CopilotTarget{Mode: "oauth"}
	exe, args := buildGhCommand(targetOAuth, "api", "/user")
	if exe != "gh" || len(args) != 2 {
		t.Errorf("buildGhCommand OAuth = %q, %v", exe, args)
	}

	targetWSL := CopilotTarget{Mode: "wsl", WslDistro: "Ubuntu"}
	exeWSL, argsWSL := buildGhCommand(targetWSL, "api", "/user")
	if exeWSL == "" || len(argsWSL) == 0 {
		t.Errorf("buildGhCommand WSL failed: %q, %v", exeWSL, argsWSL)
	}
}

func TestDefaultAllowancesForPlan(t *testing.T) {
	tests := []struct {
		plan          string
		wantActions   float64
		wantCodespace float64
	}{
		{plan: "free", wantActions: 2000, wantCodespace: 120},
		{plan: "pro", wantActions: 3000, wantCodespace: 180},
		{plan: "PRO", wantActions: 3000, wantCodespace: 180},
		{plan: "team", wantActions: 3000, wantCodespace: 180},
		{plan: "enterprise", wantActions: 50000, wantCodespace: 180},
		{plan: "unknown", wantActions: 2000, wantCodespace: 120},
		{plan: "", wantActions: 2000, wantCodespace: 120},
	}

	for _, tc := range tests {
		if got := defaultActionsMinutesForPlan(tc.plan); got != tc.wantActions {
			t.Errorf("defaultActionsMinutesForPlan(%q) = %v, want %v", tc.plan, got, tc.wantActions)
		}
		if got := defaultCodespacesHoursForPlan(tc.plan); got != tc.wantCodespace {
			t.Errorf("defaultCodespacesHoursForPlan(%q) = %v, want %v", tc.plan, got, tc.wantCodespace)
		}
	}
}

func TestCopilotToDisplayWSLWithProPlan(t *testing.T) {
	data := readFixture(t, "copilot-usage.json")
	usage, err := ParseCopilotUsage(data)
	if err != nil {
		t.Fatalf("ParseCopilotUsage() error = %v", err)
	}
	usage.Status = StatusConnected
	usage.FetchedAt = "2026-09-20T12:00:00Z"
	usage.AccountPlan = "pro"
	usage.HasActionsBilling = true
	// included minutes left 0 to verify fallback to pro plan allowance (3000)
	usage.ActionsIncludedMinutes = 0
	usage.ActionsMinutesUsed = 300
	usage.HasCodespacesBilling = true
	// included hours left 0 to verify fallback to pro plan allowance (180)
	usage.CodespacesIncludedHours = 0
	usage.CodespacesHoursUsed = 10

	display := usage.ToDisplay()
	if display.Status != StatusConnected {
		t.Fatalf("display.Status = %q, want %q", display.Status, StatusConnected)
	}
	if len(display.Groups) != 3 {
		t.Fatalf("display.Groups count = %d, want 3", len(display.Groups))
	}

	// Actions: (3000 - 300) / 3000 = 2700 / 3000 = 90%
	b1 := display.Groups[1].Buckets[0]
	if b1.Label != "monthly" || b1.Detail != "2700/3000m" || b1.Remaining != 90.0 {
		t.Errorf("Actions bucket = %+v, want monthly with 2700/3000m and 90%%", b1)
	}

	// Codespaces: (180 - 10) / 180 = 170 / 180 = 94.4%
	b2 := display.Groups[2].Buckets[0]
	if b2.Label != "monthly" || b2.Detail != "170/180h" || b2.Remaining != 94.4 {
		t.Errorf("Codespaces bucket = %+v, want monthly with 170/180h and 94.4%%", b2)
	}
}

type copilotWslMockRunner struct {
	handlers map[string]commandResult
	calls    [][]string
}

func (m *copilotWslMockRunner) run(_ context.Context, name string, args ...string) (commandResult, error) {
	m.calls = append(m.calls, append([]string{name}, args...))
	cmdLine := strings.Join(args, " ")
	var bestKey string
	for key := range m.handlers {
		if strings.Contains(cmdLine, key) {
			if len(key) > len(bestKey) {
				bestKey = key
			}
		}
	}
	if bestKey != "" {
		return m.handlers[bestKey], nil
	}
	return commandResult{ExitCode: 0}, nil
}

func TestGetCopilotWslUsageWithAccountPlan(t *testing.T) {
	ctx := context.Background()

	runner := &copilotWslMockRunner{
		handlers: map[string]commandResult{
			"auth status": {
				ExitCode: 0,
				Stdout:   "Logged in to github.com account testpro",
			},
			"/copilot_internal/user": {
				ExitCode: 0,
				Stdout:   `{"copilot_plan":"individual","quota_reset_date":"2026-10-01T00:00:00Z","quota_snapshots":{"premium_interactions":{"entitlement":500,"remaining":375,"percent_remaining":75.0}}}`,
			},
			"/user": {
				ExitCode: 0,
				Stdout:   `{"login":"testpro","plan":{"name":"pro"}}`,
			},
			"/settings/billing/usage/summary": {
				ExitCode: 0,
				Stdout:   `{"user":"testpro","usageItems":[{"product":"Actions","grossQuantity":300},{"product":"Codespaces","grossQuantity":10}]}`,
			},
			"/settings/billing/actions": {
				ExitCode: 0,
				Stdout:   `{"total_minutes_used":300,"included_minutes":3500}`,
			},
		},
	}

	deps := providerDeps{runner: runner}
	usage := getCopilotWslUsage(ctx, deps, CopilotTarget{Mode: "wsl"})

	if usage.Status != StatusConnected {
		t.Fatalf("usage.Status = %q, want %q", usage.Status, StatusConnected)
	}
	if usage.AccountPlan != "pro" {
		t.Errorf("usage.AccountPlan = %q, want %q", usage.AccountPlan, "pro")
	}
	// Since /settings/billing/actions provided 3500, it should be preferred over the default 3000
	if usage.ActionsIncludedMinutes != 3500 {
		t.Errorf("usage.ActionsIncludedMinutes = %v, want 3500", usage.ActionsIncludedMinutes)
	}
	// Codespaces should be dynamically set to 180 for pro plan
	if usage.CodespacesIncludedHours != 180 {
		t.Errorf("usage.CodespacesIncludedHours = %v, want 180", usage.CodespacesIncludedHours)
	}
	if usage.ActionsMinutesUsed != 300 {
		t.Errorf("usage.ActionsMinutesUsed = %v, want 300", usage.ActionsMinutesUsed)
	}
	if usage.CodespacesHoursUsed != 10 {
		t.Errorf("usage.CodespacesHoursUsed = %v, want 10", usage.CodespacesHoursUsed)
	}
}

func TestGetCopilotWslUsagePlanFallbackWhenActions410(t *testing.T) {
	ctx := context.Background()

	runner := &copilotWslMockRunner{
		handlers: map[string]commandResult{
			"auth status": {
				ExitCode: 0,
				Stdout:   "Logged in to github.com account testpro",
			},
			"/copilot_internal/user": {
				ExitCode: 0,
				Stdout:   `{"copilot_plan":"individual","quota_reset_date":"2026-10-01T00:00:00Z"}`,
			},
			"/user": {
				ExitCode: 0,
				Stdout:   `{"login":"testpro","plan":{"name":"pro"}}`,
			},
			"/settings/billing/usage/summary": {
				ExitCode: 0,
				Stdout:   `{"user":"testpro","usageItems":[{"product":"Actions","grossQuantity":120}]}`,
			},
			"/settings/billing/actions": {
				ExitCode: 1,
				Stdout:   `{"message":"This endpoint has been moved.","status":"410"}`,
			},
		},
	}

	deps := providerDeps{runner: runner}
	usage := getCopilotWslUsage(ctx, deps, CopilotTarget{Mode: "wsl"})

	if usage.Status != StatusConnected {
		t.Fatalf("usage.Status = %q, want %q", usage.Status, StatusConnected)
	}
	if usage.AccountPlan != "pro" {
		t.Errorf("usage.AccountPlan = %q, want %q", usage.AccountPlan, "pro")
	}
	// Fallback to Pro plan allowance: 3000 min for Actions, 180h for Codespaces
	if usage.ActionsIncludedMinutes != 3000 {
		t.Errorf("usage.ActionsIncludedMinutes = %v, want 3000", usage.ActionsIncludedMinutes)
	}
	if usage.CodespacesIncludedHours != 180 {
		t.Errorf("usage.CodespacesIncludedHours = %v, want 180", usage.CodespacesIncludedHours)
	}
	if usage.ActionsMinutesUsed != 120 {
		t.Errorf("usage.ActionsMinutesUsed = %v, want 120", usage.ActionsMinutesUsed)
	}
}
