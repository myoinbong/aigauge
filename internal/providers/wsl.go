package providers

import (
	"strings"
)

// ParseWSLDistroList extracts distribution names from `wsl.exe -l -q` output,
// handling UTF-16LE null bytes and filtering internal Docker Desktop utility distros.
func ParseWSLDistroList(raw []byte) []string {
	cleaned := make([]byte, 0, len(raw))
	for _, b := range raw {
		if b != 0 && b != '\r' {
			cleaned = append(cleaned, b)
		}
	}
	lines := strings.Split(string(cleaned), "\n")
	var distros []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "docker-desktop") {
			distros = append(distros, trimmed)
		}
	}
	return distros
}

// WSLDistroInfo represents an installed WSL distribution and whether it is the system default.
type WSLDistroInfo struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

// GetWSLDistros queries installed WSL distributions. On Windows, it reads from
// the registry directly without spawning wsl.exe, avoiding any ConPTY/openconsole.exe flashes.
func GetWSLDistros() []WSLDistroInfo {
	return getWSLDistrosPlatform()
}
