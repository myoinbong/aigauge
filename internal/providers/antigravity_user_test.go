package providers

import (
	"os"
	"testing"
)

func TestExtractAntigravityEmail_Valid(t *testing.T) {
	fixture := []byte(`
some random binary prefix \x00\x01\x02
antigravityAuthStatus{"name":"Inbong Myo (myoinbong)","apiKey":"ya29.secret","email":"user@gmail.com","userStatusProtoBinaryBase64":"xyz"}
some trailing binary bytes
`)
	email := extractAntigravityEmail(fixture)
	if email != "user@gmail.com" {
		t.Fatalf("expected user@gmail.com, got %q", email)
	}
}

func TestExtractAntigravityEmail_NotFound(t *testing.T) {
	fixture := []byte("no auth status marker in this data")
	email := extractAntigravityEmail(fixture)
	if email != "" {
		t.Fatalf("expected empty string, got %q", email)
	}
}

func TestExtractAntigravityEmail_Empty(t *testing.T) {
	email := extractAntigravityEmail(nil)
	if email != "" {
		t.Fatalf("expected empty string, got %q", email)
	}
}

func TestAntigravityUsage_ToDisplay_PreservesUser(t *testing.T) {
	usage := AntigravityUsage{
		User:            "user@gmail.com",
		DiagnosisFields: DiagnosisFields{Status: StatusConnected},
		Groups: []AntigravityUsageGroup{
			{
				DisplayName: "Gemini",
				Buckets: []AntigravityUsageBucket{
					{
						DisplayName:       "daily",
						Window:            "24h",
						RemainingFraction: 0.8,
					},
				},
			},
		},
	}
	display := usage.ToDisplay()
	if display.User != "user@gmail.com" {
		t.Fatalf("expected display.User user@gmail.com, got %q", display.User)
	}
	if display.Email != "user@gmail.com" {
		t.Fatalf("expected display.Email user@gmail.com, got %q", display.Email)
	}
}

func TestResolveAntigravityUser_Smoke(t *testing.T) {
	nativeEmail := resolveAntigravityUser(AgyTarget{Mode: "native"})
	t.Logf("resolveAntigravityUser native returned: %q", nativeEmail)
	wslEmail := resolveAntigravityUser(AgyTarget{Mode: "wsl", WslDistro: "Ubuntu"})
	t.Logf("resolveAntigravityUser wsl returned: %q", wslEmail)
}

func TestParseEmailFromIDToken(t *testing.T) {
	// payload: {"email":"blindting.ana@gmail.com","name":"myo blind"}
	// base64url: eyJlbWFpbCI6ImJsaW5kdGluZy5hbmFAZ21haWwuY29tIiwibmFtZSI6Im15byBibGluZCJ9
	token := "header.eyJlbWFpbCI6ImJsaW5kdGluZy5hbmFAZ21haWwuY29tIiwibmFtZSI6Im15byBibGluZCJ9.signature"
	email := parseEmailFromIDToken(token)
	if email != "blindting.ana@gmail.com" {
		t.Fatalf("expected blindting.ana@gmail.com, got %q", email)
	}

	// malformed
	if got := parseEmailFromIDToken("invalid"); got != "" {
		t.Fatalf("expected empty for invalid token, got %q", got)
	}
	if got := parseEmailFromIDToken("head.badbase64!!.sig"); got != "" {
		t.Fatalf("expected empty for bad base64, got %q", got)
	}
}

func TestReadEmailFromOAuthTokenFile(t *testing.T) {
	// Valid token file fixture
	tokenPayload := "eyJlbWFpbCI6IndzbC51c2VyQGdtYWlsLmNvbSIsIm5hbWUiOiJXU0wgVXNlciJ9"
	content := `{"auth_method":"oauth","id_token":"head.` + tokenPayload + `.sig"}`
	tmp := t.TempDir()
	path := tmp + "/antigravity-oauth-token"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	email := readEmailFromOAuthTokenFile(path)
	if email != "wsl.user@gmail.com" {
		t.Fatalf("expected wsl.user@gmail.com, got %q", email)
	}

	// Nonexistent file
	if got := readEmailFromOAuthTokenFile(tmp + "/nonexistent"); got != "" {
		t.Fatalf("expected empty for nonexistent file, got %q", got)
	}
}
