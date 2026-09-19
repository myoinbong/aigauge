//go:build !windows

package providers

func getWSLDistrosPlatform() []WSLDistroInfo {
	return nil
}
