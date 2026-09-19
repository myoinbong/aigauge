package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFlashTrackerCommandLifecycle(t *testing.T) {
	tracker := newFlashTracker()
	defer func() {
		if tracker.logFile != nil {
			_ = tracker.logFile.Close()
			_ = os.Remove(tracker.logPath)
		}
	}()

	cmdID := tracker.BeginCommand("agy", "-p", "/usage")
	tracker.SetCommandPID(cmdID, 12345)

	active := tracker.GetActiveCommandSnapshot()
	if active == nil {
		t.Fatal("expected active command snapshot, got nil")
	}
	if active.PID != 12345 || active.Name != "agy" || len(active.Args) != 2 {
		t.Fatalf("unexpected active command: %+v", active)
	}

	summary := FormatCommandSummary(active.Name, active.Args)
	if !strings.Contains(summary, "agy") || !strings.Contains(summary, "/usage") {
		t.Fatalf("unexpected summary format: %s", summary)
	}

	tracker.EndCommand(cmdID)
	if postEnd := tracker.GetActiveCommandSnapshot(); postEnd != nil {
		t.Fatalf("expected nil active command after EndCommand, got %+v", postEnd)
	}
}

func TestFlashTrackerRecordAndStats(t *testing.T) {
	tempLog := filepath.Join(t.TempDir(), "test_flashes.jsonl")
	t.Setenv("AGY_PROBE_LOG_PATH", tempLog)

	tracker := newFlashTracker()
	defer func() {
		if tracker.logFile != nil {
			_ = tracker.logFile.Close()
		}
	}()

	stats0 := tracker.GetStats()
	if stats0.TotalFlashes != 0 {
		t.Fatalf("initial TotalFlashes = %d, want 0", stats0.TotalFlashes)
	}

	event := FlashEvent{
		Command:      "agy.exe -p /usage",
		CommandPID:   1234,
		OwnerPID:     5678,
		OwnerProcess: "conhost.exe",
		WindowClass:  "ConsoleWindowClass",
		WindowTitle:  "agy.exe",
		Visible:      true,
		WindowRect: Rect{
			Left:   0,
			Top:    0,
			Right:  800,
			Bottom: 600,
			Width:  800,
			Height: 600,
		},
	}

	tracker.RecordFlash(event)
	time.Sleep(10 * time.Millisecond)

	stats1 := tracker.GetStats()
	if stats1.TotalFlashes != 1 {
		t.Fatalf("TotalFlashes after record = %d, want 1", stats1.TotalFlashes)
	}
	if stats1.LastFlashEvent == nil || stats1.LastFlashEvent.OwnerProcess != "conhost.exe" {
		t.Fatalf("unexpected LastFlashEvent: %+v", stats1.LastFlashEvent)
	}

	// Verify file content was written as JSON
	if tracker.logPath != "" {
		data, err := os.ReadFile(tracker.logPath)
		if err != nil {
			t.Fatalf("failed to read log file: %v", err)
		}
		var parsed FlashEvent
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("log file did not contain valid JSON FlashEvent: %v (raw: %s)", err, string(data))
		}
		if parsed.WindowClass != "ConsoleWindowClass" || parsed.OwnerPID != 5678 {
			t.Fatalf("parsed event mismatch: %+v", parsed)
		}
	}
}
