//go:build windows

package providers

import (
	"encoding/json"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	advapi32      = windows.NewLazySystemDLL("advapi32.dll")
	procCredReadW = advapi32.NewProc("CredReadW")
	procCredFree  = advapi32.NewProc("CredFree")
)

type credentialWindows struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func readWindowsCredential(target string) ([]byte, error) {
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return nil, err
	}
	var credPtr *credentialWindows
	// CRED_TYPE_GENERIC = 1
	r1, _, err := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		1,
		0,
		uintptr(unsafe.Pointer(&credPtr)),
	)
	if r1 == 0 {
		return nil, err
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credPtr)))

	if credPtr.CredentialBlobSize == 0 || credPtr.CredentialBlob == nil {
		return nil, nil
	}
	blob := make([]byte, credPtr.CredentialBlobSize)
	copy(blob, unsafe.Slice(credPtr.CredentialBlob, credPtr.CredentialBlobSize))
	return blob, nil
}

// readAntigravityCredential reads the live Google OAuth ID token from Windows Credential Manager.
func readAntigravityCredential() string {
	blob, err := readWindowsCredential("gemini:antigravity")
	if err != nil || len(blob) == 0 {
		return ""
	}
	var credData struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(blob, &credData); err != nil {
		return ""
	}
	return parseEmailFromIDToken(credData.IDToken)
}
