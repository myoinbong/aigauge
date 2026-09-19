package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// FlashEvent represents a detected unexpected window creation/visibility event.
type FlashEvent struct {
	Timestamp      string  `json:"timestamp"`
	IntervalSec    float64 `json:"intervalSec,omitempty"`
	Command        string  `json:"command,omitempty"`
	CommandPID     int     `json:"commandPid,omitempty"`
	OwnerPID       uint32  `json:"ownerPid"`
	OwnerProcess   string  `json:"ownerProcess,omitempty"`
	ParentPID      uint32  `json:"parentPid,omitempty"`
	WindowClass    string  `json:"windowClass"`
	WindowTitle    string  `json:"windowTitle"`
	WindowRect     Rect    `json:"windowRect"`
	Visible        bool    `json:"visible"`
	DurationMs     int64   `json:"durationMs,omitempty"`
	DiagnosisNotes string  `json:"notes,omitempty"`
}

type Rect struct {
	Left   int32 `json:"left"`
	Top    int32 `json:"top"`
	Right  int32 `json:"right"`
	Bottom int32 `json:"bottom"`
	Width  int32 `json:"width"`
	Height int32 `json:"height"`
}

// FlashStats provides a snapshot of detection statistics for the UI and diagnostics.
type FlashStats struct {
	TotalFlashes   int64       `json:"totalFlashes"`
	LastFlashTime  string      `json:"lastFlashTime,omitempty"`
	LastFlashEvent *FlashEvent `json:"lastFlashEvent,omitempty"`
	LogPath        string      `json:"logPath"`
}

// CommandContext holds metadata about an actively running CLI command.
type CommandContext struct {
	ID        int64
	Name      string
	Args      []string
	PID       int
	StartTime time.Time
}

// FlashTracker monitors and logs unexpected window flash events.
type FlashTracker struct {
	mu             sync.Mutex
	logFile        *os.File
	logPath        string
	totalFlashes   atomic.Int64
	lastFlashTime  time.Time
	lastFlashEvent *FlashEvent
	activeCommands map[int64]*CommandContext
	nextCommandID  int64
}

var globalTracker *FlashTracker

func init() {
	globalTracker = newFlashTracker()
	initPlatformTracker(globalTracker)
}

// GetGlobalFlashTracker returns the singleton tracker instance.
func GetGlobalFlashTracker() *FlashTracker {
	return globalTracker
}

func resolveLogPath() string {
	if custom := os.Getenv("AGY_PROBE_LOG_PATH"); custom != "" {
		_ = os.MkdirAll(filepath.Dir(custom), 0o755)
		return custom
	}

	// 1. Check if running inside or from the project workspace (find go.mod or .git)
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates, exeDir, filepath.Dir(exeDir), filepath.Dir(filepath.Dir(exeDir)))
	}

	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			projectLogDir := filepath.Join(dir, "logs")
			if err := os.MkdirAll(projectLogDir, 0o755); err == nil {
				return filepath.Join(projectLogDir, "window_flashes.jsonl")
			}
		}
	}

	// 2. Fallback to OS user config directory (%APPDATA%/agy-probe/logs)
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	logDir := filepath.Join(configDir, "agy-probe", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	return filepath.Join(logDir, "window_flashes.jsonl")
}

func newFlashTracker() *FlashTracker {
	var f *os.File
	logPath := os.Getenv("AGY_PROBE_LOG_PATH")
	if logPath != "" {
		f, _ = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	}

	return &FlashTracker{
		logFile:        f,
		logPath:        logPath,
		activeCommands: make(map[int64]*CommandContext),
	}
}

// BeginCommand registers the start of a CLI command so window events can be correlated.
func (ft *FlashTracker) BeginCommand(name string, args ...string) int64 {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.nextCommandID++
	id := ft.nextCommandID
	ft.activeCommands[id] = &CommandContext{
		ID:        id,
		Name:      name,
		Args:      args,
		StartTime: time.Now(),
	}
	return id
}

// SetCommandPID updates the PID of the running command once spawned.
func (ft *FlashTracker) SetCommandPID(id int64, pid int) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if cmd, ok := ft.activeCommands[id]; ok {
		cmd.PID = pid
	}
}

// EndCommand unregisters a finished command.
func (ft *FlashTracker) EndCommand(id int64) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	delete(ft.activeCommands, id)
}

// GetActiveCommandSnapshot returns the most recent active command, if any.
func (ft *FlashTracker) GetActiveCommandSnapshot() *CommandContext {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	var latest *CommandContext
	for _, cmd := range ft.activeCommands {
		if latest == nil || cmd.StartTime.After(latest.StartTime) {
			latest = cmd
		}
	}
	if latest == nil {
		return nil
	}
	cp := *latest
	return &cp
}

// RecordFlash records a flash event to the JSONL log and updates stats.
func (ft *FlashTracker) RecordFlash(event FlashEvent) {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	now := time.Now()
	if !ft.lastFlashTime.IsZero() {
		event.IntervalSec = now.Sub(ft.lastFlashTime).Seconds()
	}
	event.Timestamp = now.Format(time.RFC3339Nano)

	ft.totalFlashes.Add(1)
	ft.lastFlashTime = now
	eventCopy := event
	ft.lastFlashEvent = &eventCopy

	if ft.logFile != nil {
		data, err := json.Marshal(event)
		if err == nil {
			_, _ = ft.logFile.Write(append(data, '\n'))
			_ = ft.logFile.Sync()
		}
	}
}

// GetStats returns the current detection statistics.
func (ft *FlashTracker) GetStats() FlashStats {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	stats := FlashStats{
		TotalFlashes: ft.totalFlashes.Load(),
		LogPath:      ft.logPath,
	}
	if !ft.lastFlashTime.IsZero() {
		stats.LastFlashTime = ft.lastFlashTime.Format(time.RFC3339)
	}
	if ft.lastFlashEvent != nil {
		cp := *ft.lastFlashEvent
		stats.LastFlashEvent = &cp
	}
	return stats
}

// FormatCommandSummary turns command name and args into a readable single string.
func FormatCommandSummary(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return fmt.Sprintf("%s %v", filepath.Base(name), args)
}
