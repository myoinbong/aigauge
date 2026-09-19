//go:build windows

package providers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wineventOutOfContext    = 0x0000
	wineventSkipOwnProcess  = 0x0002
	eventObjectShow         = 0x8002
	eventObjectDestroy      = 0x8001
	objidWindow             = 0
	processQueryLimitedInfo = 0x1000
)

var (
	user32                    = windows.NewLazySystemDLL("user32.dll")
	procSetWinEventHook       = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent        = user32.NewProc("UnhookWinEvent")
	procGetWindowThreadProcId = user32.NewProc("GetWindowThreadProcessId")
	procGetClassNameW         = user32.NewProc("GetClassNameW")
	procGetWindowTextW        = user32.NewProc("GetWindowTextW")
	procGetWindowRect         = user32.NewProc("GetWindowRect")
	procIsWindowVisible       = user32.NewProc("IsWindowVisible")
	procGetMessageW           = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessageW      = user32.NewProc("DispatchMessageW")
	kernel32Dll               = windows.NewLazySystemDLL("kernel32.dll")
	procQueryFullProcessImgW  = kernel32Dll.NewProc("QueryFullProcessImageNameW")

	activeWindowShowTimes   sync.Map // map[uintptr]time.Time (hwnd -> showTime)
	platformTrackerInstance *FlashTracker
)

type tagRECT struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type tagMSG struct {
	Hwnd    uintptr
	Message uint32
	Wparam  uintptr
	Lparam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

func initPlatformTracker(ft *FlashTracker) {
	platformTrackerInstance = ft
	go startWinEventLoop(ft)
}

func startWinEventLoop(ft *FlashTracker) {
	// Must run message loop on a dedicated locked OS thread
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	callback := syscall.NewCallback(func(hWinEventHook uintptr, event uint32, hwnd uintptr, idObject int32, idChild int32, dwEventThread uint32, dwmsEventTime uint32) uintptr {
		handleWinEvent(ft, event, hwnd, idObject, idChild)
		return 0
	})

	hook, _, _ := procSetWinEventHook.Call(
		uintptr(eventObjectDestroy),
		uintptr(eventObjectShow),
		0,
		callback,
		0,
		0,
		uintptr(wineventOutOfContext|wineventSkipOwnProcess),
	)

	if hook == 0 {
		return
	}
	defer procUnhookWinEvent.Call(hook)

	var msg tagMSG
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func handleWinEvent(ft *FlashTracker, event uint32, hwnd uintptr, idObject, idChild int32) {
	if idObject != objidWindow || hwnd == 0 {
		return
	}

	if event == eventObjectDestroy {
		activeWindowShowTimes.Delete(hwnd)
		return
	}

	if event != eventObjectShow {
		return
	}

	// ONLY inspect windows created while agy-probe has an active background command running!
	// If probe is idle, never monitor, touch, or log any user windows/terminals.
	activeCmd := ft.GetActiveCommandSnapshot()
	if activeCmd == nil {
		return
	}

	var pid uint32
	procGetWindowThreadProcId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 || pid == uint32(os.Getpid()) {
		return
	}

	isCmdChild := activeCmd.PID > 0 && pid == uint32(activeCmd.PID)

	procName := getProcessImageName(pid)
	procBase := strings.ToLower(filepath.Base(procName))
	isSuspectProcess := isCmdChild ||
		strings.Contains(procBase, "conhost") ||
		strings.Contains(procBase, "openconsole") ||
		strings.Contains(procBase, "agy")

	className := getWindowClassName(hwnd)
	isPseudoConsole := strings.EqualFold(className, "PseudoConsoleWindow")
	isConsole := strings.EqualFold(className, "ConsoleWindowClass") || isPseudoConsole

	// Only capture if it's a console window related to our active command or child processes
	if !isConsole && !isSuspectProcess {
		return
	}

	title := getWindowText(hwnd)
	rect := getWindowRect(hwnd)
	visible := isWindowVisible(hwnd)

	showTime := time.Now()
	activeWindowShowTimes.Store(hwnd, showTime)

	flashEvent := FlashEvent{
		Command:      FormatCommandSummary(activeCmd.Name, activeCmd.Args),
		CommandPID:   activeCmd.PID,
		OwnerPID:     pid,
		OwnerProcess: procBase,
		WindowClass:  className,
		WindowTitle:  title,
		WindowRect: Rect{
			Left:   rect.Left,
			Top:    rect.Top,
			Right:  rect.Right,
			Bottom: rect.Bottom,
			Width:  rect.Right - rect.Left,
			Height: rect.Bottom - rect.Top,
		},
		Visible: visible,
	}

	ft.RecordFlash(flashEvent)
}

func getWindowClassName(hwnd uintptr) string {
	buf := make([]uint16, 256)
	ret, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if ret == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:ret])
}

func getWindowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	ret, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if ret == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:ret])
}

func getWindowRect(hwnd uintptr) tagRECT {
	var rect tagRECT
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	return rect
}

func isWindowVisible(hwnd uintptr) bool {
	ret, _, _ := procIsWindowVisible.Call(hwnd)
	return ret != 0
}

func getProcessImageName(pid uint32) string {
	hProc, err := windows.OpenProcess(processQueryLimitedInfo, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(hProc)

	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	ret, _, _ := procQueryFullProcessImgW.Call(
		uintptr(hProc),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}
