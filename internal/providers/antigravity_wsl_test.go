package providers

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type testRunner struct {
	fn func(ctx context.Context, name string, args ...string) (commandResult, error)
}

func (r *testRunner) run(ctx context.Context, name string, args ...string) (commandResult, error) {
	if r.fn != nil {
		return r.fn(ctx, name, args...)
	}
	return commandResult{}, nil
}

func TestBuildAgyCommandNative(t *testing.T) {
	target := AgyTarget{Mode: "native"}
	exe, args := buildAgyCommand(target, "agy.exe", "-p", "/usage", "--output-format", "json")
	if exe != "agy.exe" {
		t.Errorf("exe = %q, want %q", exe, "agy.exe")
	}
	wantArgs := []string{"-p", "/usage", "--output-format", "json"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestBuildAgyCommandWSLDefault(t *testing.T) {
	target := AgyTarget{Mode: "wsl"}
	exe, args := buildAgyCommand(target, "", "-p", "/usage", "--output-format", "json")
	if exe != "wsl.exe" {
		t.Errorf("exe = %q, want %q", exe, "wsl.exe")
	}
	wantArgs := []string{"--exec", "/bin/bash", "-lc", `exec agy "$@"`, "_", "-p", "/usage", "--output-format", "json"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestBuildAgyCommandWSLDistro(t *testing.T) {
	target := AgyTarget{Mode: "wsl", WslDistro: "Ubuntu-24.04"}
	exe, args := buildAgyCommand(target, "", "-p", "/usage")
	if exe != "wsl.exe" {
		t.Errorf("exe = %q, want %q", exe, "wsl.exe")
	}
	wantArgs := []string{"-d", "Ubuntu-24.04", "--exec", "/bin/bash", "-lc", `exec agy "$@"`, "_", "-p", "/usage"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestDiagnoseAntigravityWSLNotFound(t *testing.T) {
	runner := &testRunner{
		fn: func(ctx context.Context, name string, args ...string) (commandResult, error) {
			if name == "wsl.exe" && strings.Contains(strings.Join(args, " "), "--version") {
				return commandResult{
					ExitCode: 127,
					Stderr:   "bash: line 1: agy: command not found\n",
				}, nil
			}
			return commandResult{ExitCode: 1}, nil
		},
	}
	target := AgyTarget{Mode: "wsl", WslDistro: "Debian"}
	diag, ok := diagnoseAntigravity(context.Background(), runner, target, "wsl.exe", false)
	if ok {
		t.Error("diagnoseAntigravity returned ok=true, want false")
	}
	if diag.Status != StatusNotInstalled {
		t.Errorf("Status = %v, want StatusNotInstalled", diag.Status)
	}
}

func TestDiagnoseAntigravityWSLSuccess(t *testing.T) {
	runner := &testRunner{
		fn: func(ctx context.Context, name string, args ...string) (commandResult, error) {
			if name == "wsl.exe" && strings.Contains(strings.Join(args, " "), "--version") {
				return commandResult{
					ExitCode: 0,
					Stdout:   "agy version 1.2.0\n",
				}, nil
			}
			return commandResult{ExitCode: 1}, nil
		},
	}
	target := AgyTarget{Mode: "wsl"}
	diag, ok := diagnoseAntigravity(context.Background(), runner, target, "wsl.exe", false)
	if ok {
		t.Error("diagnoseAntigravity returned ok=true for inactive probe, want false with StatusAuthCheckRequired")
	}
	if diag.Status != StatusAuthCheckRequired {
		t.Errorf("Status = %v, want StatusAuthCheckRequired", diag.Status)
	}
}

func TestParseWSLDistroList(t *testing.T) {
	// Simulated UTF-16LE bytes with null bytes and docker distros
	raw := []byte("U\x00b\x00u\x00n\x00t\x00u\x00-\x002\x004\x00.\x000\x004\x00\r\x00\n\x00d\x00o\x00c\x00k\x00e\x00r\x00-\x00d\x00e\x00s\x00k\x00t\x00o\x00p\x00\r\x00\n\x00D\x00e\x00b\x00i\x00a\x00n\x00\r\x00\n\x00")
	got := ParseWSLDistroList(raw)
	want := []string{"Ubuntu-24.04", "Debian"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseWSLDistroList() = %#v, want %#v", got, want)
	}
}
