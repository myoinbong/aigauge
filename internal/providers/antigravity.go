package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// antigravityInstallGuideURL answers both "how do I install this" and "how do
// I get a newer version".
const antigravityInstallGuideURL = "https://antigravity.google/docs/cli/install/"

// antigravityAuthMarkers are the phrases that let a failed agy command be read
// as "signed out" rather than "something went wrong". The list is
// deliberately short: an unrecognized failure must fall through to a
// temporary error, since telling a signed-in user they are signed out is the
// worse mistake.
var antigravityAuthMarkers = []string{
	"not logged in",
	"not signed in",
	"unauthorized",
	"authentication required",
	"authentication failed",
	"please log in",
	"please sign in",
	"login required",
	"401",
}

const (
	// antigravityUsageTimeout bounds the whole `/usage` run. It stays well above
	// the 30s --print-timeout handed to agy itself so the CLI gets the chance to
	// report its own timeout before this one kills it.
	antigravityUsageTimeout = 45 * time.Second
)

// buildAgyCommand constructs the executable and argument slice for either native or WSL execution.
func buildAgyCommand(target AgyTarget, agyPath string, args ...string) (string, []string) {
	if target.Mode == "wsl" {
		wslArgs := make([]string, 0, len(args)+6)
		if target.WslDistro != "" {
			wslArgs = append(wslArgs, "-d", target.WslDistro)
		}
		wslArgs = append(wslArgs, "--exec", "/bin/bash", "-lc", `exec agy "$@"`, "_")
		wslArgs = append(wslArgs, args...)
		return "wsl.exe", wslArgs
	}
	return agyPath, args
}

func runAgyTarget(ctx context.Context, runner commandRunner, target AgyTarget, agyPath string, args ...string) (commandResult, error) {
	suppressAgyUpdaterFlashes(target)
	exe, cmdArgs := buildAgyCommand(target, agyPath, args...)
	return runner.run(ctx, exe, cmdArgs...)
}

func suppressAgyUpdaterFlashes(target AgyTarget) {
	if target.Mode == "wsl" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	timestampPath := filepath.Join(home, ".gemini", "antigravity-cli", "last_check.timestamp")
	now := time.Now()
	if err := os.Chtimes(timestampPath, now, now); err != nil {
		dir := filepath.Dir(timestampPath)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			_ = os.WriteFile(timestampPath, nil, 0o644)
		}
	}
}

func findAgyTarget(target AgyTarget, deps providerDeps) (string, Diagnosis, bool) {
	if target.Mode == "wsl" {
		wslPath, _ := resolveExecutable("wsl.exe", nil, deps)
		if wslPath == "" {
			wslPath = "wsl.exe"
		}
		return wslPath, Diagnosis{}, true
	}
	return findAgy(deps)
}

// EnsureAntigravityCLI verifies that agy is installed and authenticated.
func EnsureAntigravityCLI() Diagnosis {
	return EnsureAntigravityCLIWithTarget(AgyTarget{Mode: "native"})
}

// EnsureAntigravityCLIWithTarget verifies that agy is installed and authenticated for the given target.
func EnsureAntigravityCLIWithTarget(target AgyTarget) Diagnosis {
	deps := defaultDeps()
	agyPath, notInstalled, found := findAgyTarget(target, deps)
	if !found {
		return notInstalled
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusCommandTimeout)
	defer cancel()
	return checkAntigravityAuth(ctx, deps.runner, target, agyPath, "agy authentication confirmed.")
}

func checkAntigravityAuth(ctx context.Context, runner commandRunner, target AgyTarget, agyPath, successMessage string) Diagnosis {
	result, err := runAgyTarget(ctx, runner, target, agyPath, "-p", "/usage", "--output-format", "json", "--print-timeout", "15s")
	if err != nil {
		return Diagnosis{Status: StatusTemporaryError, Message: "Could not check Antigravity authentication.", Details: technicalDetails(err.Error())}
	}
	details := strings.TrimSpace(result.Stderr)
	if details == "" {
		details = strings.TrimSpace(result.Stdout)
	}
	if result.ExitCode != 0 {
		if containsAntigravityAuthMarker(details) || strings.Contains(strings.ToLower(details), "sign in") {
			return Diagnosis{Status: StatusLoginRequired, Message: "Sign in with agy first, then try again.", Details: technicalDetails(details)}
		}
		if containsAnyMarker(details, unsupportedCLIMarkers) {
			return Diagnosis{Status: StatusUnsupportedCLI, Message: unsupportedCLIMessage("Antigravity CLI", antigravityInstallGuideURL), Details: technicalDetails(details)}
		}
		return Diagnosis{Status: StatusTemporaryError, Message: "Could not check Antigravity authentication.", Details: technicalDetails(details)}
	}
	return Diagnosis{Status: StatusConnected, Message: successMessage}
}

func containsAntigravityAuthMarker(message string) bool {
	lower := strings.ToLower(message)
	for _, marker := range antigravityAuthMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ParseAntigravityUsage unmarshals agy's raw `/usage` stdout and converts it
// into AI Gauge's own shape - see AntigravityUsage's doc comment.
func ParseAntigravityUsage(data []byte) (AntigravityUsage, error) {
	var response agyUsageResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return AntigravityUsage{}, err
	}

	usage := AntigravityUsage{
		Description: response.Command.Data.Description,
		Raw:         append(json.RawMessage(nil), data...),
	}
	for _, group := range response.Command.Data.Groups {
		g := AntigravityUsageGroup{DisplayName: group.Name, Description: group.Description}
		for _, bucket := range group.Buckets {
			g.Buckets = append(g.Buckets, AntigravityUsageBucket{
				BucketID:          bucket.ID,
				DisplayName:       bucket.Name,
				Window:            bucket.Window,
				Description:       bucket.Description,
				RemainingFraction: bucket.RemainingFraction,
				ResetTime:         bucket.ResetTime,
			})
		}
		usage.Groups = append(usage.Groups, g)
	}
	return usage, nil
}

var antigravityWindowOrder = map[string]int{"5h": 0, "24h": 1, "weekly": 2}
var antigravityWindowLabels = map[string]string{"5h": "5h", "weekly": "7d"}

func antigravityOrder(window string) int {
	if order, ok := antigravityWindowOrder[window]; ok {
		return order
	}
	return len(antigravityWindowOrder)
}

// ToDisplay resolves each bucket's window into a display label and a stable
// 5h/24h/weekly order, and its fraction into a percentage.
func (u AntigravityUsage) ToDisplay() DisplayUsage {
	display := DisplayUsage{
		FetchedAt:       u.FetchedAt,
		DiagnosisFields: u.DiagnosisFields,
		User:            u.User,
		Email:           u.User,
	}
	if u.Status != StatusConnected {
		return display
	}
	for _, group := range u.Groups {
		buckets := append([]AntigravityUsageBucket(nil), group.Buckets...)
		sort.SliceStable(buckets, func(i, j int) bool {
			return antigravityOrder(buckets[i].Window) < antigravityOrder(buckets[j].Window)
		})
		displayGroup := DisplayUsageGroup{Name: group.DisplayName}
		for _, bucket := range buckets {
			label := bucket.DisplayName
			if l, ok := antigravityWindowLabels[bucket.Window]; ok {
				label = l
			}
			displayGroup.Buckets = append(displayGroup.Buckets, DisplayUsageBucket{
				Label:     label,
				Remaining: bucket.RemainingFraction * 100,
				ResetTime: bucket.ResetTime,
			})
		}
		display.Groups = append(display.Groups, displayGroup)
	}
	display.Status = StatusConnected
	return display
}

// GetAntigravityUsage runs the real, agy-CLI-backed usage lookup for native target.
func GetAntigravityUsage(tokenKey string) AntigravityUsage {
	return GetAntigravityUsageWithTarget(tokenKey, AgyTarget{Mode: "native"})
}

// GetAntigravityUsageWithTarget runs the agy-CLI-backed usage lookup for the given target (native or WSL).
func GetAntigravityUsageWithTarget(tokenKey string, target AgyTarget) AntigravityUsage {
	return getAntigravityUsage(context.Background(), defaultDeps(), tokenKey, target, true)
}

// FetchAntigravityRawUsage returns agy's unconverted `/usage` stdout for native target.
func FetchAntigravityRawUsage(tokenKey string) ([]byte, error) {
	return FetchAntigravityRawUsageWithTarget(tokenKey, AgyTarget{Mode: "native"})
}

// FetchAntigravityRawUsageWithTarget returns agy's unconverted `/usage` stdout for the given target.
func FetchAntigravityRawUsageWithTarget(_ string, target AgyTarget) ([]byte, error) {
	deps := defaultDeps()
	agyPath, notInstalled, found := findAgyTarget(target, deps)
	if !found {
		return nil, errors.New(notInstalled.Message)
	}

	ctx, cancel := context.WithTimeout(context.Background(), antigravityUsageTimeout)
	defer cancel()
	result, err := runAgyTarget(ctx, deps.runner, target, agyPath, "-p", "/usage", "--output-format", "json", "--print-timeout", "30s")
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("agy exited %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return []byte(result.Stdout), nil
}

// findAgy resolves the agy executable: PATH first, then the platform's
// fallback install location. The raw lookup failure is kept under Details for
// transparency, while Message provides clear guidance.
func findAgy(deps providerDeps) (string, Diagnosis, bool) {
	path, fallback := resolveExecutable("agy", antigravityFallbackPath, deps)
	if path == "" {
		return "", Diagnosis{
			Status:  StatusNotInstalled,
			Message: `Install the Antigravity CLI (<code>agy</code>) and log in to monitor your quota. <a href="` + antigravityInstallGuideURL + `">Installation guide</a>`,
			Details: technicalDetails(notFoundDetails(fallback)),
		}, false
	}
	return path, Diagnosis{}, true
}

func diagnoseAntigravityLocal(ctx context.Context, deps providerDeps, tokenKey string) Diagnosis {
	return diagnoseAntigravityLocalWithTarget(ctx, deps, tokenKey, AgyTarget{Mode: "native"})
}

func diagnoseAntigravityLocalWithTarget(ctx context.Context, deps providerDeps, _ string, target AgyTarget) Diagnosis {
	agyPath, notInstalled, ok := findAgyTarget(target, deps)
	if !ok {
		return notInstalled
	}
	diagnosis, _ := diagnoseAntigravity(ctx, deps.runner, target, agyPath, false)
	return diagnosis
}

func diagnoseAntigravity(ctx context.Context, runner commandRunner, target AgyTarget, agyPath string, active bool) (Diagnosis, bool) {
	ctx, cancel := context.WithTimeout(ctx, statusCommandTimeout)
	defer cancel()
	result, err := runAgyTarget(ctx, runner, target, agyPath, "--version")
	if err != nil {
		return Diagnosis{
			Status:  StatusTemporaryError,
			Message: "Could not run the Antigravity CLI. Try again.",
			Details: technicalDetails(err.Error() + " " + result.Stderr),
		}, false
	}
	if result.ExitCode != 0 {
		out := result.Stdout + " " + result.Stderr
		if target.Mode == "wsl" && (strings.Contains(strings.ToLower(out), "not found") || strings.Contains(strings.ToLower(out), "no such file")) {
			distroInfo := ""
			if target.WslDistro != "" {
				distroInfo = " in WSL distro " + target.WslDistro
			}
			return Diagnosis{
				Status:  StatusNotInstalled,
				Message: fmt.Sprintf(`Install the Antigravity CLI (<code>agy</code>)%s and log in to monitor your quota. <a href="%s">Installation guide</a>`, distroInfo, antigravityInstallGuideURL),
				Details: technicalDetails(out),
			}, false
		}
		if containsAnyMarker(out, unsupportedCLIMarkers) {
			return Diagnosis{
				Status:  StatusUnsupportedCLI,
				Message: unsupportedCLIMessage("Antigravity CLI", antigravityInstallGuideURL),
				Details: technicalDetails(out),
			}, false
		}
		return Diagnosis{
			Status:  StatusUnsupportedCLI,
			Message: unsupportedCLIMessage("Antigravity CLI", antigravityInstallGuideURL),
			Details: technicalDetails(result.Stdout + " " + result.Stderr),
		}, false
	}

	if !active {
		targetDesc := agyPath
		if target.Mode == "wsl" {
			targetDesc = "WSL"
			if target.WslDistro != "" {
				targetDesc += fmt.Sprintf(" (%s)", target.WslDistro)
			}
		}
		return Diagnosis{
			Status:  StatusAuthCheckRequired,
			Message: "Antigravity CLI found. Connect to verify usage.",
			Details: technicalDetails(fmt.Sprintf("Found agy (%s) via %s", strings.TrimSpace(result.Stdout), targetDesc)),
		}, false
	}
	return Diagnosis{}, true
}

func getAntigravityUsage(ctx context.Context, deps providerDeps, _ string, target AgyTarget, active bool) AntigravityUsage {
	usage := AntigravityUsage{FetchedAt: time.Now().Format(time.RFC3339)}

	agyPath, notInstalled, found := findAgyTarget(target, deps)
	if !found {
		usage.applyDiagnosis(notInstalled)
		return usage
	}

	if !active {
		diagnosis, ok := diagnoseAntigravity(ctx, deps.runner, target, agyPath, false)
		if !ok {
			usage.applyDiagnosis(diagnosis)
			return usage
		}
	}

	usageCtx, cancel := context.WithTimeout(ctx, antigravityUsageTimeout)
	defer cancel()
	result, runErr := runAgyTarget(usageCtx, deps.runner, target, agyPath,
		"-p", "/usage", "--output-format", "json", "--print-timeout", "30s")
	if runErr != nil {
		usage.applyDiagnosis(Diagnosis{
			Status:  StatusTemporaryError,
			Message: "Could not reach Antigravity right now. Retry in a moment.",
			Details: technicalDetails(runErr.Error() + " " + result.Stderr),
		})
		return usage
	}

	parsed, diagnosis := classifyAntigravityUsage(result)
	if diagnosis.Status == StatusConnected {
		usage.Groups = parsed.Groups
		usage.Description = parsed.Description
		usage.User = resolveAntigravityUser(target)
	}
	usage.applyDiagnosis(diagnosis)
	return usage
}

func classifyAntigravityUsage(result commandResult) (AntigravityUsage, Diagnosis) {
	if result.ExitCode == 0 {
		parsed, err := ParseAntigravityUsage([]byte(result.Stdout))
		if err == nil && len(parsed.Groups) > 0 {
			return parsed, Diagnosis{Status: StatusConnected}
		}
		reason := ReasonNoUsageData
		if err != nil {
			reason = ReasonUnsupportedResponse
		}
		return AntigravityUsage{}, usageUnreadableDiagnosis("Antigravity", reason, err)
	}

	output := strings.TrimSpace(result.Stdout + " " + result.Stderr)
	if containsAntigravityAuthMarker(output) || strings.Contains(strings.ToLower(output), "sign in") {
		return AntigravityUsage{}, Diagnosis{
			Status:  StatusLoginRequired,
			Message: "Log in to the Antigravity CLI to view quota information.",
			Details: technicalDetails(output),
		}
	}
	if containsAnyMarker(output, unsupportedCLIMarkers) {
		return AntigravityUsage{}, Diagnosis{
			Status:  StatusUnsupportedCLI,
			Message: unsupportedCLIMessage("Antigravity CLI", antigravityInstallGuideURL),
			Details: technicalDetails(output),
		}
	}
	return AntigravityUsage{}, Diagnosis{
		Status:  StatusTemporaryError,
		Message: "Could not reach Antigravity right now. Retry in a moment.",
		Details: technicalDetails(output),
	}
}
