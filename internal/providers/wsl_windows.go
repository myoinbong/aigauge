//go:build windows

package providers

import (
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// getWSLDistrosPlatform enumerates installed WSL distributions directly from
// the Windows Registry (HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss).
// This completely avoids spawning wsl.exe, which prevents Windows Terminal
// ConPTY (openconsole.exe) from creating and flashing a PseudoConsoleWindow.
func getWSLDistrosPlatform() []WSLDistroInfo {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Lxss`, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer key.Close()

	defaultGuid, _, _ := key.GetStringValue("DefaultDistribution")
	defaultGuid = strings.ToLower(strings.TrimSpace(defaultGuid))

	subkeys, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}

	var distros []WSLDistroInfo
	for _, sub := range subkeys {
		subKey, err := registry.OpenKey(key, sub, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		name, _, err := subKey.GetStringValue("DistributionName")
		subKey.Close()
		if err == nil {
			trimmed := strings.TrimSpace(name)
			if trimmed != "" && !strings.HasPrefix(trimmed, "docker-desktop") {
				isDefault := strings.ToLower(strings.TrimSpace(sub)) == defaultGuid
				distros = append(distros, WSLDistroInfo{
					Name:      trimmed,
					IsDefault: isDefault,
				})
			}
		}
	}

	// Sort default distro first, then alphabetically
	sort.SliceStable(distros, func(i, j int) bool {
		if distros[i].IsDefault != distros[j].IsDefault {
			return distros[i].IsDefault
		}
		return distros[i].Name < distros[j].Name
	})

	return distros
}
