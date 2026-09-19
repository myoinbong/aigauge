package providers

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var (
	// authStatusEmailRegex extracts the email from the JSON structure inside state.vscdb:
	// antigravityAuthStatus{"name":"...","email":"user@gmail.com",...}
	authStatusEmailRegex = regexp.MustCompile(`antigravityAuthStatus.*?["']email["']\s*:\s*["']([^"']+)["']`)
)

func parseEmailFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	payloadSegment := parts[1]
	decoded, err := base64.RawURLEncoding.DecodeString(payloadSegment)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(payloadSegment)
		if err != nil {
			return ""
		}
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}
	return claims.Email
}

// extractAntigravityEmail parses user email from binary data (e.g. state.vscdb).
func extractAntigravityEmail(data []byte) string {
	if match := authStatusEmailRegex.FindSubmatch(data); len(match) > 1 {
		return string(match[1])
	}
	return ""
}

func readEmailFromOAuthTokenFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	var tokenData struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(data, &tokenData); err == nil && tokenData.IDToken != "" {
		if email := parseEmailFromIDToken(tokenData.IDToken); email != "" {
			return email
		}
	}
	return extractAntigravityEmail(data)
}

func readVscdbSnippet(path string, maxBytes int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		return nil
	}

	reader := io.LimitReader(f, maxBytes)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil
	}
	return data
}

// candidateNativeVscdbPaths returns potential state.vscdb locations for native Windows target.
func candidateNativeVscdbPaths() []string {
	var paths []string

	// 1. Windows standard AppData
	if appData := os.Getenv("APPDATA"); appData != "" {
		paths = append(paths,
			filepath.Join(appData, "Antigravity", "User", "globalStorage", "state.vscdb"),
			filepath.Join(appData, "Antigravity IDE", "User", "globalStorage", "state.vscdb"),
		)
	}

	// 2. User home directory fallback
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths,
			filepath.Join(home, "AppData", "Roaming", "Antigravity", "User", "globalStorage", "state.vscdb"),
			filepath.Join(home, "AppData", "Roaming", "Antigravity IDE", "User", "globalStorage", "state.vscdb"),
		)
	}

	// 3. If running inside WSL environment (tests or dev), check mounted Windows drives (/mnt/c/Users/...)
	if runtime.GOOS == "linux" {
		userDirs, _ := filepath.Glob("/mnt/c/Users/*/AppData/Roaming/Antigravity/User/globalStorage/state.vscdb")
		paths = append(paths, userDirs...)
		ideDirs, _ := filepath.Glob("/mnt/c/Users/*/AppData/Roaming/Antigravity IDE/User/globalStorage/state.vscdb")
		paths = append(paths, ideDirs...)
	}

	return paths
}

func resolveNativeAntigravityUser() string {
	// 1. Primary: Read live OAuth identity from Windows Credential Manager
	if email := readAntigravityCredential(); email != "" {
		return email
	}

	// 2. Fallback: Search local state.vscdb files
	paths := candidateNativeVscdbPaths()
	const maxReadSize = 10 * 1024 * 1024 // 10MB limit

	for _, path := range paths {
		if path == "" {
			continue
		}
		data := readVscdbSnippet(path, maxReadSize)
		if len(data) == 0 {
			continue
		}
		if email := extractAntigravityEmail(data); email != "" {
			return email
		}
	}
	return ""
}

func resolveWslAntigravityUser(target AgyTarget) string {
	// 1. If running natively inside Linux/WSL:
	if runtime.GOOS == "linux" {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			tokenPath := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
			if email := readEmailFromOAuthTokenFile(tokenPath); email != "" {
				return email
			}
			linuxPaths := []string{
				filepath.Join(home, ".config", "Antigravity", "User", "globalStorage", "state.vscdb"),
				filepath.Join(home, ".config", "Antigravity IDE", "User", "globalStorage", "state.vscdb"),
			}
			for _, p := range linuxPaths {
				if data := readVscdbSnippet(p, 10*1024*1024); len(data) > 0 {
					if email := extractAntigravityEmail(data); email != "" {
						return email
					}
				}
			}
		}
		return ""
	}

	// 2. If running on Windows host, inspect WSL filesystem via UNC shares
	distros := []string{}
	if target.WslDistro != "" {
		distros = append(distros, target.WslDistro)
	} else {
		for _, info := range GetWSLDistros() {
			distros = append(distros, info.Name)
		}
		if len(distros) == 0 {
			distros = append(distros, "Ubuntu")
		}
	}

	prefixes := []string{`\\wsl.localhost`, `\\wsl$`}
	for _, distro := range distros {
		for _, prefix := range prefixes {
			// A. OAuth token file: \\wsl.localhost\<distro>\home\*\.gemini\antigravity-cli\antigravity-oauth-token
			tokenPattern := filepath.Join(prefix, distro, "home", "*", ".gemini", "antigravity-cli", "antigravity-oauth-token")
			if matches, _ := filepath.Glob(tokenPattern); len(matches) > 0 {
				for _, m := range matches {
					if email := readEmailFromOAuthTokenFile(m); email != "" {
						return email
					}
				}
			}

			// B. Fallback: \\wsl.localhost\<distro>\home\*\.config\Antigravity*\User\globalStorage\state.vscdb
			vscdbPattern := filepath.Join(prefix, distro, "home", "*", ".config", "Antigravity*", "User", "globalStorage", "state.vscdb")
			if matches, _ := filepath.Glob(vscdbPattern); len(matches) > 0 {
				for _, m := range matches {
					if data := readVscdbSnippet(m, 10*1024*1024); len(data) > 0 {
						if email := extractAntigravityEmail(data); email != "" {
							return email
						}
					}
				}
			}
		}
	}

	return ""
}

// resolveAntigravityUser searches credentials based on target environment (native vs WSL).
func resolveAntigravityUser(target AgyTarget) string {
	if target.Mode == "wsl" {
		return resolveWslAntigravityUser(target)
	}
	return resolveNativeAntigravityUser()
}
