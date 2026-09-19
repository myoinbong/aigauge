//go:build !windows

package providers

// readAntigravityCredential returns empty string on non-Windows platforms.
func readAntigravityCredential() string {
	return ""
}
