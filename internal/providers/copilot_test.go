package providers

import (
	"context"
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
