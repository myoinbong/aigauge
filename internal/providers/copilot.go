package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"
)

// buildGhCommand constructs the executable and argument slice for either native or WSL execution.
func buildGhCommand(target CopilotTarget, args ...string) (string, []string) {
	if target.Mode == "wsl" {
		if runtime.GOOS != "windows" {
			return "gh", args
		}
		wslArgs := make([]string, 0, len(args)+6)
		if target.WslDistro != "" {
			wslArgs = append(wslArgs, "-d", target.WslDistro)
		}
		wslArgs = append(wslArgs, "--exec", "/bin/bash", "-lc", `exec gh "$@"`, "_")
		wslArgs = append(wslArgs, args...)
		return "wsl.exe", wslArgs
	}
	return "gh", args
}

func runGhTarget(ctx context.Context, runner commandRunner, target CopilotTarget, args ...string) (commandResult, error) {
	exe, cmdArgs := buildGhCommand(target, args...)
	return runner.run(ctx, exe, cmdArgs...)
}

// ParseCopilotUsage unmarshals a raw GitHub Copilot internal user response.
func ParseCopilotUsage(data []byte) (CopilotUsage, error) {
	var usage CopilotUsage
	if err := json.Unmarshal(data, &usage); err != nil {
		return CopilotUsage{}, err
	}
	usage.Raw = append(json.RawMessage(nil), data...)
	return usage, nil
}

// defaultActionsMinutesForPlan returns the included GitHub Actions minutes for a given account plan.
func defaultActionsMinutesForPlan(plan string) float64 {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "pro", "team":
		return 3000
	case "enterprise":
		return 50000
	default:
		return 2000
	}
}

// defaultCodespacesHoursForPlan returns the included Codespaces core hours for a given account plan.
func defaultCodespacesHoursForPlan(plan string) float64 {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "pro", "team", "enterprise":
		return 180
	default:
		return 120
	}
}

// ToDisplay converts Copilot's quota snapshots and GitHub billing metrics into display buckets.
func (u CopilotUsage) ToDisplay() DisplayUsage {
	display := DisplayUsage{
		FetchedAt:       u.FetchedAt,
		DiagnosisFields: u.DiagnosisFields,
		User:            u.User,
		Email:           u.User,
	}
	if u.Status != StatusConnected {
		return display
	}

	display.Plan = u.CopilotPlan
	if display.Plan == "" && u.AccountPlan != "" {
		display.Plan = u.AccountPlan
	}

	if u.CopilotPlan == "" && u.AccountPlan == "" && len(u.QuotaSnapshots) == 0 && !u.HasActionsBilling {
		display.applyDiagnosis(usageUnreadableDiagnosis("GitHub", ReasonNoUsageData, fmt.Errorf("no quota snapshots available in response")))
		return display
	}

	// When billing is available (WSL mode), render separate titled groups
	if u.HasActionsBilling {
		var groups []DisplayUsageGroup

		// Group 1: Copilot
		copilotBucket := DisplayUsageBucket{Label: "monthly"}
		if snapshot, ok := u.QuotaSnapshots["premium_interactions"]; ok {
			copilotBucket.Remaining = snapshot.PercentRemaining
			copilotBucket.ResetTime = u.QuotaResetDate
			if snapshot.Entitlement > 0 {
				copilotBucket.Detail = fmt.Sprintf("%d/%d", int(snapshot.Remaining), int(snapshot.Entitlement))
			}
		} else if u.QuotaResetDate != "" {
			copilotBucket.ResetTime = u.QuotaResetDate
		}
		groups = append(groups, DisplayUsageGroup{
			Name:    "Copilot",
			Buckets: []DisplayUsageBucket{copilotBucket},
		})

		// Group 2: GitHub Actions
		incActions := u.ActionsIncludedMinutes
		if incActions <= 0 {
			incActions = defaultActionsMinutesForPlan(u.AccountPlan)
		}
		usedActions := u.ActionsMinutesUsed
		remActions := incActions - usedActions
		if remActions < 0 {
			remActions = 0
		}
		pctActions := (remActions / incActions) * 100
		if pctActions > 100 {
			pctActions = 100
		}
		if pctActions < 0 {
			pctActions = 0
		}
		pctActions = float64(int(pctActions*10+0.5)) / 10
		actionsBucket := DisplayUsageBucket{
			Label:     "monthly",
			Remaining: pctActions,
			Detail:    fmt.Sprintf("%d/%dm", int(remActions), int(incActions)),
			ResetTime: u.QuotaResetDate,
		}
		groups = append(groups, DisplayUsageGroup{
			Name:    "GitHub Actions",
			Buckets: []DisplayUsageBucket{actionsBucket},
		})

		// Group 3: Codespace
		if u.HasCodespacesBilling {
			incCodespaces := u.CodespacesIncludedHours
			if incCodespaces <= 0 {
				incCodespaces = defaultCodespacesHoursForPlan(u.AccountPlan)
			}
			usedCodespaces := u.CodespacesHoursUsed
			remCodespaces := incCodespaces - usedCodespaces
			if remCodespaces < 0 {
				remCodespaces = 0
			}
			pctCodespaces := (remCodespaces / incCodespaces) * 100
			if pctCodespaces > 100 {
				pctCodespaces = 100
			}
			if pctCodespaces < 0 {
				pctCodespaces = 0
			}
			pctCodespaces = float64(int(pctCodespaces*10+0.5)) / 10
			codespaceBucket := DisplayUsageBucket{
				Label:     "monthly",
				Remaining: pctCodespaces,
				Detail:    fmt.Sprintf("%d/%dh", int(remCodespaces), int(incCodespaces)),
				ResetTime: u.QuotaResetDate,
			}
			groups = append(groups, DisplayUsageGroup{
				Name:    "Codespace",
				Buckets: []DisplayUsageBucket{codespaceBucket},
			})
		}

		display.Groups = groups
		display.Status = StatusConnected
		return display
	}

	// OAuth mode: single flat group without title
	bucket := DisplayUsageBucket{Label: "monthly"}
	if snapshot, ok := u.QuotaSnapshots["premium_interactions"]; ok {
		bucket.Remaining = snapshot.PercentRemaining
		bucket.ResetTime = u.QuotaResetDate
		if snapshot.Entitlement > 0 {
			bucket.Detail = fmt.Sprintf("%d/%d", int(snapshot.Remaining), int(snapshot.Entitlement))
		}
	} else if u.QuotaResetDate != "" {
		bucket.ResetTime = u.QuotaResetDate
	}
	display.Groups = []DisplayUsageGroup{{Buckets: []DisplayUsageBucket{bucket}}}
	display.Status = StatusConnected
	return display
}

// GetCopilotUsage fetches the usage for the GitHub Copilot instance identified by tokenKey.
func GetCopilotUsage(tokenKey string) CopilotUsage {
	return GetCopilotUsageWithTarget(tokenKey, CopilotTarget{Mode: "oauth"})
}

// GetCopilotUsageWithTarget fetches Copilot and billing usage according to target.
func GetCopilotUsageWithTarget(tokenKey string, target CopilotTarget) CopilotUsage {
	return getCopilotUsageWithTarget(context.Background(), defaultDeps(), tokenKey, target, true)
}

func getCopilotUsageWithTarget(ctx context.Context, deps providerDeps, tokenKey string, target CopilotTarget, active bool) CopilotUsage {
	if target.Mode == "wsl" {
		return getCopilotWslUsage(ctx, deps, target)
	}
	return getCopilotUsage(ctx, deps, tokenKey, active)
}

func getCopilotWslUsage(ctx context.Context, deps providerDeps, target CopilotTarget) CopilotUsage {
	usage := CopilotUsage{
		FetchedAt: time.Now().Format(time.RFC3339),
	}

	// Check WSL gh status first
	diag := diagnoseCopilotWsl(ctx, deps, target)
	if diag.Status != StatusConnected {
		usage.applyDiagnosis(diag)
		return usage
	}

	// 1. Fetch Copilot internal quota
	copilotRes, err := runGhTarget(ctx, deps.runner, target, "api", "/copilot_internal/user")
	copilotSuccess := false
	var username string
	if err == nil && copilotRes.ExitCode == 0 && strings.TrimSpace(copilotRes.Stdout) != "" {
		if parsed, parseErr := ParseCopilotUsage([]byte(copilotRes.Stdout)); parseErr == nil {
			usage = parsed
			usage.FetchedAt = time.Now().Format(time.RFC3339)
			copilotSuccess = true
			var userObj struct {
				Login string `json:"login"`
			}
			_ = json.Unmarshal([]byte(copilotRes.Stdout), &userObj)
			username = userObj.Login
		}
	}

	// 2. Fetch authenticated user profile to get account plan and username
	var accountPlan string
	userRes, userErr := runGhTarget(ctx, deps.runner, target, "api", "/user")
	if userErr == nil && userRes.ExitCode == 0 && strings.TrimSpace(userRes.Stdout) != "" {
		var userObj struct {
			Login string `json:"login"`
			Plan  struct {
				Name string `json:"name"`
			} `json:"plan"`
		}
		if jsonErr := json.Unmarshal([]byte(userRes.Stdout), &userObj); jsonErr == nil {
			if username == "" && userObj.Login != "" {
				username = userObj.Login
			}
			accountPlan = userObj.Plan.Name
			usage.AccountPlan = accountPlan
		}
	}

	// 3. Fetch billing summary if username is available
	billingSuccess := false
	if username != "" {
		billingRes, billingErr := runGhTarget(ctx, deps.runner, target, "api", "-H", "Accept: application/vnd.github+json", fmt.Sprintf("/users/%s/settings/billing/usage/summary", username))
		if billingErr == nil && billingRes.ExitCode == 0 && strings.TrimSpace(billingRes.Stdout) != "" {
			var summary GitHubBillingUsageSummary
			if jsonErr := json.Unmarshal([]byte(billingRes.Stdout), &summary); jsonErr == nil {
				billingSuccess = true
				usage.HasActionsBilling = true
				usage.ActionsIncludedMinutes = defaultActionsMinutesForPlan(accountPlan)
				usage.HasCodespacesBilling = true
				usage.CodespacesIncludedHours = defaultCodespacesHoursForPlan(accountPlan)
				for _, item := range summary.UsageItems {
					if strings.EqualFold(item.Product, "Actions") {
						usage.ActionsMinutesUsed += item.GrossQuantity
					} else if strings.EqualFold(item.Product, "Codespaces") {
						usage.CodespacesHoursUsed += item.GrossQuantity
					}
				}
			}
		}

		// Also check /settings/billing/actions in case included_minutes is directly provided
		actionsRes, actionsErr := runGhTarget(ctx, deps.runner, target, "api", "-H", "Accept: application/vnd.github+json", fmt.Sprintf("/users/%s/settings/billing/actions", username))
		if actionsErr == nil && actionsRes.ExitCode == 0 && strings.TrimSpace(actionsRes.Stdout) != "" {
			var actionsBilling GitHubActionsBilling
			if jsonErr := json.Unmarshal([]byte(actionsRes.Stdout), &actionsBilling); jsonErr == nil {
				if actionsBilling.IncludedMinutes > 0 {
					usage.ActionsIncludedMinutes = actionsBilling.IncludedMinutes
					usage.HasActionsBilling = true
					billingSuccess = true
				}
			}
		}
	}

	// 4. Fetch primary email if available
	emailsRes, emailsErr := runGhTarget(ctx, deps.runner, target, "api", "/user/emails")
	if emailsErr == nil && emailsRes.ExitCode == 0 && strings.TrimSpace(emailsRes.Stdout) != "" {
		var emails []struct {
			Email   string `json:"email"`
			Primary bool   `json:"primary"`
		}
		if json.Unmarshal([]byte(emailsRes.Stdout), &emails) == nil {
			for _, e := range emails {
				if e.Primary && e.Email != "" {
					usage.User = e.Email
					break
				}
			}
		}
	}
	if usage.User == "" && username != "" {
		usage.User = username
	}

	if !copilotSuccess && !billingSuccess {
		if err != nil {
			usage.applyDiagnosis(usageFailureDiagnosis("GitHub Copilot (WSL)", err))
		} else {
			usage.applyDiagnosis(usageFailureDiagnosis("GitHub Copilot (WSL)", fmt.Errorf("failed to fetch copilot or billing data from gh cli")))
		}
		return usage
	}

	usage.Status = StatusConnected
	return usage
}

// FetchCopilotRawUsage returns the unconverted usage response for the Copilot
// instance whose token is stored under tokenKey.
func FetchCopilotRawUsage(tokenKey string) ([]byte, error) {
	ctx := context.Background()
	deps := defaultDeps()
	diagnosis, credentials, ok := diagnoseCopilot(ctx, deps, tokenKey, true)
	if !ok {
		return nil, fmt.Errorf("%s", diagnosis.Message)
	}
	accessToken, err := authorizedAccessToken(ctx, deps, "copilot", tokenKey, credentials.Tokens.AccessToken)
	if err != nil {
		return nil, err
	}
	return fetchAuthorizedJSON(ctx, "https://api.github.com/copilot_internal/user", "GitHub Copilot", map[string]string{
		"Authorization": "Bearer " + accessToken,
		"Accept":        "application/json",
		"User-Agent":    "AI-Gauge",
	})
}

func getCopilotUsage(ctx context.Context, deps providerDeps, tokenKey string, active bool) CopilotUsage {
	usage := CopilotUsage{FetchedAt: time.Now().Format(time.RFC3339)}

	diagnosis, credentials, ok := diagnoseCopilot(ctx, deps, tokenKey, active)
	if !ok {
		usage.applyDiagnosis(diagnosis)
		return usage
	}

	accessToken, err := authorizedAccessToken(ctx, deps, "copilot", tokenKey, credentials.Tokens.AccessToken)
	if err != nil {
		usage.applyDiagnosis(usageFailureDiagnosis("GitHub Copilot", err))
		return usage
	}

	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
		"Accept":        "application/json",
		"User-Agent":    "AI-Gauge",
	}

	data, err := fetchAuthorizedJSON(ctx, "https://api.github.com/copilot_internal/user", "GitHub Copilot", headers)
	if err != nil {
		usage.applyDiagnosis(usageFailureDiagnosis("GitHub Copilot", err))
		return usage
	}

	parsed, err := ParseCopilotUsage(data)
	if err != nil {
		usage.applyDiagnosis(usageUnreadableDiagnosis("GitHub Copilot", ReasonUnsupportedResponse, err))
		return usage
	}

	// Try fetching primary email from /user/emails, falling back to /user
	if emailsData, err := fetchAuthorizedJSON(ctx, "https://api.github.com/user/emails", "GitHub Copilot", headers); err == nil {
		var emails []struct {
			Email   string `json:"email"`
			Primary bool   `json:"primary"`
		}
		if json.Unmarshal(emailsData, &emails) == nil {
			for _, e := range emails {
				if e.Primary && e.Email != "" {
					parsed.User = e.Email
					break
				}
			}
		}
	}
	if parsed.User == "" {
		if userData, err := fetchAuthorizedJSON(ctx, "https://api.github.com/user", "GitHub Copilot", headers); err == nil {
			var userObj struct {
				Email string `json:"email"`
				Login string `json:"login"`
			}
			if json.Unmarshal(userData, &userObj) == nil {
				if userObj.Email != "" {
					parsed.User = userObj.Email
				} else {
					parsed.User = userObj.Login
				}
			}
		}
	}

	parsed.FetchedAt = usage.FetchedAt
	parsed.Status = StatusConnected
	return parsed
}
