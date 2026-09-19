//go:build !windows

package providers

func initPlatformTracker(_ *FlashTracker) {
	// No-op on non-Windows platforms
}
