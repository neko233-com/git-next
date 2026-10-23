// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"
)

type desktopRect struct{ left, top, right, bottom int32 }
type desktopPoint struct{ x, y int32 }
type desktopInput struct {
	kind, padding uint32
	data          [32]byte
}
type desktopWindow struct {
	handle uintptr
	pid    uint32
}

func desktopWait(seconds float64, action func() (bool, error)) error {
	end := time.Now().Add(time.Duration(seconds * float64(time.Second)))
	for time.Now().Before(end) {
		ok, e := action()
		if e != nil {
			return e
		}
		if ok {
			return nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	return fmt.Errorf("owned terminal action did not complete in time")
}
func desktopWindowFor(pid uint32) desktopWindow {
	var found uintptr
	callback := windows.NewCallback(func(hwnd, unused uintptr) uintptr {
		var actual uint32
		desktopCall(desktopUser, "GetWindowThreadProcessId", hwnd, uintptr(unsafe.Pointer(&actual)))
		if actual == pid && desktopCall(desktopUser, "IsWindowVisible", hwnd) != 0 {
			found = hwnd
			return 0
		}
		return 1
	})
	desktopCall(desktopUser, "EnumWindows", callback, 0)
	return desktopWindow{found, pid}
}
func (w desktopWindow) owned() error {
	if w.handle == 0 {
		return fmt.Errorf("owned terminal window missing")
	}
	var actual uint32
	desktopCall(desktopUser, "GetWindowThreadProcessId", w.handle, uintptr(unsafe.Pointer(&actual)))
	if actual != w.pid {
		return fmt.Errorf("window ownership changed; input refused")
	}
	return nil
}
func (w desktopWindow) activate() error {
	if e := w.owned(); e != nil {
		return e
	}
	var actual uint32
	target := desktopCall(desktopUser, "GetWindowThreadProcessId", w.handle, uintptr(unsafe.Pointer(&actual)))
	desktopCall(desktopUser, "ShowWindow", w.handle, 9)
	current := desktopCall(desktopKernel, "GetCurrentThreadId")
	foreground := desktopCall(desktopUser, "GetForegroundWindow")
	foregroundThread := uintptr(0)
	if foreground != 0 {
		foregroundThread = desktopCall(desktopUser, "GetWindowThreadProcessId", foreground, 0)
	}
	attached := []uintptr{}
	seen := map[uintptr]bool{}
	for _, thread := range []uintptr{target, foregroundThread} {
		if thread == 0 || thread == current || seen[thread] {
			continue
		}
		seen[thread] = true
		if desktopCall(desktopUser, "AttachThreadInput", current, thread, 1) != 0 {
			attached = append(attached, thread)
		}
	}
	desktopCall(desktopUser, "SetForegroundWindow", w.handle)
	desktopCall(desktopUser, "SetFocus", w.handle)
	for i := len(attached) - 1; i >= 0; i-- {
		desktopCall(desktopUser, "AttachThreadInput", current, attached[i], 0)
	}
	return desktopWait(2, func() (bool, error) { return desktopCall(desktopUser, "GetForegroundWindow") == w.handle, nil })
}
func (w desktopWindow) focused() error {
	if e := w.owned(); e != nil {
		return e
	}
	if desktopCall(desktopUser, "GetForegroundWindow") != w.handle {
		if e := w.activate(); e != nil {
			return e
		}
	}
	if desktopCall(desktopUser, "GetForegroundWindow") != w.handle {
		return fmt.Errorf("focus changed; no input sent to another application")
	}
	return nil
}
func (w desktopWindow) send(events []desktopInput) error {
	if e := w.focused(); e != nil {
		return e
	}
	if len(events) == 0 {
		return nil
	}
	if desktopCall(desktopUser, "SendInput", uintptr(len(events)), uintptr(unsafe.Pointer(&events[0])), unsafe.Sizeof(desktopInput{})) != uintptr(len(events)) {
		return fmt.Errorf("owned window input was refused")
	}
	return nil
}
func desktopKey(vk uint16, up bool) desktopInput {
	i := desktopInput{kind: 1}
	binary.LittleEndian.PutUint16(i.data[:], vk)
	if up {
		binary.LittleEndian.PutUint32(i.data[4:], 2)
	}
	return i
}
func desktopMouse(flags, data uint32) desktopInput {
	i := desktopInput{}
	binary.LittleEndian.PutUint32(i.data[8:], data)
	binary.LittleEndian.PutUint32(i.data[12:], flags)
	return i
}
func (w desktopWindow) shortcut(keys ...uint16) error {
	down, up := []desktopInput{}, []desktopInput{}
	for _, k := range keys {
		down = append(down, desktopKey(k, false))
	}
	for i := len(keys) - 1; i >= 0; i-- {
		up = append(up, desktopKey(keys[i], true))
	}
	if e := w.send(down); e != nil {
		return e
	}
	time.Sleep(35 * time.Millisecond)
	return w.send(up)
}
func (w desktopWindow) message(message, wparam, lparam uintptr) (uintptr, error) {
	if e := w.owned(); e != nil {
		return 0, e
	}
	var answer uintptr
	if desktopCall(desktopUser, "SendMessageTimeoutW", w.handle, message, wparam, lparam, 3, 2000, uintptr(unsafe.Pointer(&answer))) == 0 {
		return 0, fmt.Errorf("owned window did not respond to message")
	}
	return answer, nil
}
func (w desktopWindow) text(text string) error {
	for _, unit := range utf16.Encode([]rune(text)) {
		if _, e := w.message(0x102, uintptr(unit), 1); e != nil {
			return e
		}
	}
	return nil
}
func (w desktopWindow) command(text string) error {
	if e := w.shortcut(0x11, 0x55); e != nil {
		return e
	}
	if e := w.text(text); e != nil {
		return e
	}
	return w.shortcut(13)
}
func (w desktopWindow) setSize(width, height int32) error {
	if e := w.owned(); e != nil {
		return e
	}
	if desktopCall(desktopUser, "SetWindowPos", w.handle, 0, 40, 40, uintptr(width), uintptr(height), 4) == 0 {
		return fmt.Errorf("owned window resize refused")
	}
	return nil
}
func (w desktopWindow) bounds(frame bool) (image.Rectangle, error) {
	if e := w.owned(); e != nil {
		return image.Rectangle{}, e
	}
	r := desktopRect{}
	if frame {
		if desktopCall(desktopUser, "GetWindowRect", w.handle, uintptr(unsafe.Pointer(&r))) == 0 {
			return image.Rectangle{}, fmt.Errorf("owned window bounds unavailable")
		}
		return image.Rect(int(r.left), int(r.top), int(r.right), int(r.bottom)), nil
	}
	p := desktopPoint{}
	if desktopCall(desktopUser, "GetClientRect", w.handle, uintptr(unsafe.Pointer(&r))) == 0 || desktopCall(desktopUser, "ClientToScreen", w.handle, uintptr(unsafe.Pointer(&p))) == 0 {
		return image.Rectangle{}, fmt.Errorf("owned client bounds unavailable")
	}
	if r.right < 500 || r.bottom < 250 {
		return image.Rectangle{}, fmt.Errorf("owned terminal unavailable for capture")
	}
	return image.Rect(int(p.x), int(p.y), int(p.x+r.right), int(p.y+r.bottom)), nil
}
func (w desktopWindow) capture(frame bool) (*image.RGBA, error) {
	if e := w.focused(); e != nil {
		return nil, e
	}
	box, e := w.bounds(frame)
	if e != nil {
		return nil, e
	}
	width, height := box.Dx(), box.Dy()
	if width <= 0 || height <= 0 || width > 8192 || height > 8192 {
		return nil, fmt.Errorf("owned capture dimensions invalid")
	}
	dc := desktopCall(desktopUser, "GetDC", 0)
	if dc == 0 {
		return nil, fmt.Errorf("screen capture DC unavailable")
	}
	defer desktopCall(desktopUser, "ReleaseDC", 0, dc)
	memory := desktopCall(desktopGDI, "CreateCompatibleDC", dc)
	if memory == 0 {
		return nil, fmt.Errorf("capture DC allocation failed")
	}
	defer desktopCall(desktopGDI, "DeleteDC", memory)
	type bitmapInfo struct {
		size                   uint32
		width, height          int32
		planes, bitCount       uint16
		compression, sizeImage uint32
		x, y                   int32
		colors, important      uint32
		mask                   [3]uint32
	}
	info := bitmapInfo{size: 40, width: int32(width), height: -int32(height), planes: 1, bitCount: 32}
	var bits *byte
	bitmap := desktopCall(desktopGDI, "CreateDIBSection", dc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		return nil, fmt.Errorf("capture bitmap allocation failed")
	}
	defer desktopCall(desktopGDI, "DeleteObject", bitmap)
	previous := desktopCall(desktopGDI, "SelectObject", memory, bitmap)
	defer desktopCall(desktopGDI, "SelectObject", memory, previous)
	if desktopCall(desktopGDI, "BitBlt", memory, 0, 0, uintptr(width), uintptr(height), dc, uintptr(box.Min.X), uintptr(box.Min.Y), 0x00CC0020|0x40000000) == 0 {
		return nil, fmt.Errorf("owned screen pixels unavailable")
	}
	desktopCall(desktopGDI, "GdiFlush")
	source := unsafe.Slice(bits, width*height*4)
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(source); i += 4 {
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = source[i+2], source[i+1], source[i], 255
	}
	return out, nil
}
func (w desktopWindow) screenshot(path string, frame bool) error {
	if e := w.activate(); e != nil {
		return e
	}
	img, e := w.capture(frame)
	if e != nil {
		return e
	}
	var buf bytes.Buffer
	if e = png.Encode(&buf, img); e != nil {
		return e
	}
	return write(path, buf.Bytes(), 0644)
}
func (w desktopWindow) promptReady() (bool, error) {
	if e := w.activate(); e != nil {
		return false, e
	}
	img, e := w.capture(false)
	if e != nil {
		return false, e
	}
	for y := 55; y < min(img.Bounds().Dy()-35, 300); y++ {
		count := 0
		for x := 14; x < 42; x++ {
			p := img.RGBAAt(x, y)
			if int(p.G) > int(p.R)+30 && int(p.G) > int(p.B)+25 {
				count++
			}
		}
		if count >= 2 {
			return true, nil
		}
	}
	return false, nil
}

type desktopProcess struct {
	cmd     *exec.Cmd
	cleanup func()
	done    chan struct{}
	once    sync.Once
	code    int
	err     error
}

func startDesktop(binary string, args, env []string) (*desktopProcess, error) {
	cmd := exec.CommandContext(context.Background(), binary, args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cleanup, e := prepareProcess(cmd)
	if e != nil {
		return nil, e
	}
	cmd.SysProcAttr.HideWindow = false // This acceptance owns and exercises a visible GUI window.
	if e = cmd.Start(); e != nil {
		cleanup()
		return nil, e
	}
	if e = attachProcess(cmd); e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cleanup()
		return nil, e
	}
	p := &desktopProcess{cmd: cmd, cleanup: cleanup, done: make(chan struct{})}
	go func() { p.err = cmd.Wait(); p.code = cmd.ProcessState.ExitCode(); close(p.done) }()
	return p, nil
}
func (p *desktopProcess) wait(seconds int) (int, error) {
	select {
	case <-p.done:
		return p.code, nil
	case <-time.After(time.Duration(seconds) * time.Second):
		return -1, fmt.Errorf("owned terminal process exceeded deadline")
	}
}
func (p *desktopProcess) close() {
	p.once.Do(func() {
		p.cleanup()
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			_ = p.cmd.Process.Kill()
		}
	})
}
func (p *desktopProcess) window() (desktopWindow, error) {
	var w desktopWindow
	e := desktopWait(12, func() (bool, error) {
		select {
		case <-p.done:
			return false, fmt.Errorf("owned terminal exited before its window appeared")
		default:
		}
		w = desktopWindowFor(uint32(p.cmd.Process.Pid))
		return w.handle != 0, nil
	})
	return w, e
}
func desktopWaitFile(path, want string, exact bool) (string, error) {
	var text string
	e := desktopWait(12, func() (bool, error) {
		b, e := os.ReadFile(path)
		if e != nil {
			return false, nil
		}
		text = string(b)
		if exact {
			return text == want, nil
		}
		return strings.TrimSpace(text) != "", nil
	})
	if e != nil {
		return "", fmt.Errorf("owned fixture file did not reach expected state: %s", filepath.Base(path))
	}
	return text, nil
}
func desktopQuote(path string) string {
	return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", `'\''`) + "'"
}
func desktopCropHash(img *image.RGBA) string {
	var out bytes.Buffer
	box := image.Rect(5, 100, min(360, img.Bounds().Dx()), img.Bounds().Dy()-45)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			p := img.RGBAAt(x, y)
			out.WriteByte(p.R)
			out.WriteByte(p.G)
			out.WriteByte(p.B)
		}
	}
	return digestBytes(out.Bytes())
}

func terminalDesktopAcceptance(a *app, o options) (returned error) {
	if e := o.require("binary", "git", "output"); e != nil {
		return e
	}
	if selected := o.get("only-case"); selected != "" {
		known := false
		for _, name := range desktopActivityCases {
			known = known || name == selected
		}
		if !known {
			return fmt.Errorf("unknown desktop case: %s", selected)
		}
	}
	releaseDesktop, e := acquireDesktopLease()
	if e != nil {
		return e
	}
	defer releaseDesktop()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	desktopCall(desktopUser, "SetProcessDPIAware")
	binary, e := absolute(o.get("binary"))
	if e != nil {
		return e
	}
	git, e := absolute(o.get("git"))
	if e != nil {
		return e
	}
	output, e := absolute(o.get("output"))
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(output), 0755); e != nil {
		return e
	}
	pasteKey := o.value("paste-key", "auto")
	if pasteKey != "auto" && pasteKey != "ctrl-shift-v" && pasteKey != "shift-insert" {
		return fmt.Errorf("invalid paste-key")
	}
	class, _ := windows.UTF16PtrFromString("STATIC")
	empty, _ := windows.UTF16PtrFromString("")
	clipboardWindow := desktopCall(desktopUser, "CreateWindowExW", 0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(empty)), 0, 0, 0, 0, 0, ^uintptr(2), 0, 0, 0)
	if clipboardWindow == 0 {
		return fmt.Errorf("owned clipboard fixture window could not be created")
	}
	defer desktopCall(desktopUser, "DestroyWindow", clipboardWindow)
	hotkeyConflict := false
	if pasteKey == "auto" {
		registered, last := desktopCallError(desktopUser, "RegisterHotKey", clipboardWindow, 0x2330, 0x4006, 0x56)
		if registered != 0 {
			desktopCall(desktopUser, "UnregisterHotKey", clipboardWindow, 0x2330)
		} else {
			hotkeyConflict = last == windows.Errno(1409)
		}
		pasteKey = "ctrl-shift-v"
		if hotkeyConflict {
			pasteKey = "shift-insert"
		}
	}
	start := time.Now()
	root, e := os.MkdirTemp("", "git-terminal-desktop-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	home, repo := filepath.Join(root, "home"), filepath.Join(root, "中文 repo")
	for _, p := range []string{home, repo} {
		if e = os.MkdirAll(p, 0755); e != nil {
			return e
		}
	}
	env := []string{}
	for _, s := range os.Environ() {
		key, _, _ := strings.Cut(s, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "GIT_") || strings.HasPrefix(upper, "GCM_") || strings.HasPrefix(upper, "SSH_") || strings.HasPrefix(upper, "GNUPG") {
			continue
		}
		env = append(env, s)
	}
	env = append(env, "HOME="+home, "USERPROFILE="+home, "APPDATA="+filepath.Join(home, "appdata"), "LOCALAPPDATA="+filepath.Join(home, "local"), "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"), "GIT_NEXT_CONFIG_HOME="+filepath.Join(home, "git-next"), "SSH_ROOT_PATH="+filepath.Join(home, ".ssh"), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "LANG=C.UTF-8", "LC_ALL=C.UTF-8")
	results := []record{}
	resizeDiagnostics := record{}
	var process *desktopProcess
	var window desktopWindow
	snapshot := &clipboardSnapshot{}
	clipboardSaved := false
	fixtureClipboard := ""
	imagePath := func(suffix string) string { return strings.TrimSuffix(output, filepath.Ext(output)) + suffix + ".png" }
	recordCase := func(name string, action func() error) {
		if o.get("only-case") != "" && name != o.get("only-case") && name != "identity-profiles-unicode-cwd-language-atomic-settings" {
			return
		}
		then := time.Now()
		if process != nil {
			w := desktopWindowFor(uint32(process.cmd.Process.Pid))
			if w.handle != 0 {
				_ = w.activate()
			}
		}
		e := action()
		item := record{"case": name, "status": "pass", "seconds": roundedSeconds(time.Since(then))}
		if e != nil {
			item["status"] = "fail"
			item["error_type"] = "AcceptanceError"
			reason := e.Error()
			if len(reason) > 180 {
				reason = reason[:180]
			}
			item["reason"] = reason
			if process != nil {
				w := desktopWindowFor(uint32(process.cmd.Process.Pid))
				if w.handle != 0 {
					_ = w.screenshot(imagePath("-"+name), false)
				}
			}
		}
		results = append(results, item)
		_ = a.json(item)
	}
	run := func(program string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		r, e := runProcess(ctx, a.root, env, nil, program, args...)
		if e != nil {
			return nil, e
		}
		if r.code != 0 {
			return nil, fmt.Errorf("owned CLI exited %d", r.code)
		}
		return r.stdout, nil
	}
	recordCase("identity-profiles-unicode-cwd-language-atomic-settings", func() error {
		out, e := run(binary, "--print-setup", "--json", "--no-profile", "--git", git, "--cwd", repo)
		if e != nil {
			return e
		}
		var info record
		if e = json.Unmarshal(out, &info); e != nil {
			return e
		}
		if info["product"] != "git-terminal" || info["renderer"] != "gpui-fast" || info["git"] == nil || info["no_profile"] != true {
			return fmt.Errorf("terminal identity/profiles differ")
		}
		for _, language := range []string{"chinese", "english", "auto"} {
			out, e = run(binary, "language", "set", language)
			if e != nil {
				return e
			}
			var value record
			if e = json.Unmarshal(out, &value); e != nil {
				return e
			}
			canonical := map[string]string{"chinese": "zh-Hans", "english": "en", "auto": "auto"}[language]
			if value["configured"] != language && value["configured"] != canonical {
				return fmt.Errorf("atomic language settings differ")
			}
		}
		return nil
	})
	cleanupMain := func() {
		if clipboardSaved {
			end := time.Now().Add(3 * time.Second)
			for {
				owner := desktopCall(desktopUser, "GetClipboardOwner")
				var ownerPID uint32
				if owner != 0 {
					desktopCall(desktopUser, "GetWindowThreadProcessId", owner, uintptr(unsafe.Pointer(&ownerPID)))
				}
				text, e := desktopClipboardText()
				if e == nil {
					pid := uint32(0)
					if process != nil {
						pid = uint32(process.cmd.Process.Pid)
					}
					if ownerPID == uint32(os.Getpid()) || ownerPID == pid || text == fixtureClipboard || strings.TrimSpace(text) == "COPY_MARKER_GIT_TERMINAL" {
						e = snapshot.restore(clipboardWindow)
						if e == nil {
							results = append(results, record{"case": "original-clipboard-restored", "status": "pass"})
							break
						}
					} else {
						results = append(results, record{"case": "new-external-clipboard-change-preserved", "status": "pass"})
						break
					}
				}
				if time.Now().After(end) {
					results = append(results, record{"case": "original-clipboard-restored", "status": "fail", "reason": "Clipboard remained busy"})
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		snapshot.close()
		if process != nil {
			w := desktopWindowFor(uint32(process.cmd.Process.Pid))
			if w.handle != 0 {
				desktopCall(desktopUser, "PostMessageW", w.handle, 0x10, 0, 0)
			}
			_, e := process.wait(15)
			status := "pass"
			if e != nil {
				status = "fail"
			}
			process.close()
			results = append(results, record{"case": "owned-window-exit-and-process-cleanup", "status": status})
		}
	}
	mainErr := func() error {
		if _, e = run(git, "init", "--initial-branch=feature/tabs", repo); e != nil {
			return e
		}
		process, e = startDesktop(binary, []string{"--no-profile", "--git", git, "--cwd", repo}, env)
		if e != nil {
			return e
		}
		window, e = process.window()
		if e != nil {
			return e
		}
		if e = window.setSize(1120, 720); e != nil {
			return e
		}
		if e = window.activate(); e != nil {
			return e
		}
		if e = desktopWait(20, window.promptReady); e != nil {
			return e
		}
		time.Sleep(500 * time.Millisecond)
		if e = window.shortcut(0x1b); e != nil {
			return e
		}
		if e = window.shortcut(0x11, 0x55); e != nil {
			return e
		}
		if e = window.screenshot(imagePath("-startup"), false); e != nil {
			return e
		}
		recordCase("native-window-wm-char-unicode-commit-and-pty", func() error {
			p := filepath.Join(root, "unicode.txt")
			if e := window.command("printf '%s' '中文🙂' > " + desktopQuote(p)); e != nil {
				return e
			}
			_, e := desktopWaitFile(p, "中文🙂", true)
			return e
		})
		recordCase("native-clipboard-paste-with-original-preserved-in-memory", func() error {
			if e := window.focused(); e != nil {
				return e
			}
			if e := snapshot.capture(); e != nil {
				return e
			}
			clipboardSaved = true
			p := filepath.Join(root, "clipboard.txt")
			fixtureClipboard = "printf '%s' 'clipboard-中文' > " + desktopQuote(p)
			if e := desktopSetClipboard(clipboardWindow, fixtureClipboard); e != nil {
				return e
			}
			text, e := desktopClipboardText()
			if e != nil {
				return e
			}
			if text != fixtureClipboard {
				return fmt.Errorf("fixture clipboard differs")
			}
			keys := []uint16{0x11, 0x10, 0x56}
			if pasteKey == "shift-insert" {
				keys = []uint16{0x10, 0x2d}
			}
			if e = window.shortcut(keys...); e != nil {
				return e
			}
			time.Sleep(200 * time.Millisecond)
			if e = window.shortcut(13); e != nil {
				return e
			}
			_, e = desktopWaitFile(p, "clipboard-中文", true)
			return e
		})
		recordCase("native-keyboard-tabs-independent-shells-and-close", func() error {
			first, second, returned, exited := filepath.Join(root, "first.txt"), filepath.Join(root, "second.txt"), filepath.Join(root, "returned.txt"), filepath.Join(root, "after-shell-exit.txt")
			if e := window.command("GIT_TERMINAL_TAB=first; printf first > " + desktopQuote(first)); e != nil {
				return e
			}
			if _, e := desktopWaitFile(first, "first", true); e != nil {
				return e
			}
			if e := window.shortcut(0x11, 0x54); e != nil {
				return e
			}
			time.Sleep(time.Second)
			if e := window.command(`printf '%s' "${GIT_TERMINAL_TAB-independent}" > ` + desktopQuote(second)); e != nil {
				return e
			}
			if _, e := desktopWaitFile(second, "independent", true); e != nil {
				return e
			}
			for _, keys := range [][]uint16{{0x11, 9}, {0x11, 0x10, 9}, {0x11, 0x57}} {
				if e := window.shortcut(keys...); e != nil {
					return e
				}
				time.Sleep(150 * time.Millisecond)
			}
			if e := window.command(`printf '%s' "$GIT_TERMINAL_TAB" > ` + desktopQuote(returned)); e != nil {
				return e
			}
			if _, e := desktopWaitFile(returned, "first", true); e != nil {
				return e
			}
			if e := window.shortcut(0x11, 0x54); e != nil {
				return e
			}
			time.Sleep(500 * time.Millisecond)
			if e := window.command("exit"); e != nil {
				return e
			}
			time.Sleep(400 * time.Millisecond)
			if e := window.command(`printf '%s' "$GIT_TERMINAL_TAB" > ` + desktopQuote(exited)); e != nil {
				return e
			}
			_, e := desktopWaitFile(exited, "first", true)
			return e
		})
		recordCase("native-window-resize-reaches-conpty", func() error {
			before, after, goFile := filepath.Join(root, "width-before.txt"), filepath.Join(root, "width-after.txt"), filepath.Join(root, "resize-go")
			script := "while [ ! -f " + desktopQuote(goFile) + " ]; do stty size > " + desktopQuote(before+".tmp") + " && mv " + desktopQuote(before+".tmp") + " " + desktopQuote(before) + "; sleep .05; done; stty size > " + desktopQuote(after+".tmp") + " && mv " + desktopQuote(after+".tmp") + " " + desktopQuote(after)
			probe, e := startDesktop(binary, []string{"--shell", "bash", "--no-profile", "--git", git, "--cwd", repo, "--command", script}, env)
			if e != nil {
				return e
			}
			defer probe.close()
			w, e := probe.window()
			if e != nil {
				return e
			}
			if e = w.setSize(1120, 720); e != nil {
				return e
			}
			if _, e = desktopWaitFile(before, "", false); e != nil {
				return e
			}
			time.Sleep(400 * time.Millisecond)
			text, e := os.ReadFile(before)
			if e != nil {
				return e
			}
			fields := strings.Fields(string(text))
			if len(fields) != 2 {
				return fmt.Errorf("initial ConPTY dimensions missing")
			}
			columns := fields[1]
			if e = w.setSize(920, 650); e != nil {
				return e
			}
			time.Sleep(400 * time.Millisecond)
			if e = write(goFile, []byte("go"), 0644); e != nil {
				return e
			}
			resized, e := desktopWaitFile(after, "", false)
			if e != nil {
				return e
			}
			next := strings.Fields(resized)
			if len(next) != 2 || columns == next[1] {
				return fmt.Errorf("window resize did not reach ConPTY")
			}
			status, e := probe.wait(10)
			if e != nil {
				return e
			}
			if status != 0 {
				return fmt.Errorf("resize probe exit %d", status)
			}
			return nil
		})
		recordCase("embedded-icon-native-caption-hit-areas-and-compact-window", func() error {
			for _, kind := range []uintptr{1, 0} {
				icon, e := window.message(0x7f, kind, 0)
				if e != nil {
					return e
				}
				if icon == 0 {
					return fmt.Errorf("embedded application icons missing")
				}
			}
			box, e := window.bounds(false)
			if e != nil {
				return e
			}
			scale := float64(desktopCall(desktopUser, "GetDpiForWindow", window.handle)) / 96
			hit := func(x, y int) uintptr { return uintptr(uint32(uint16(x)) | uint32(uint16(y))<<16) }
			for _, item := range [][2]int{{23, 20}, {69, 9}, {115, 8}} {
				x, y := box.Max.X-int(float64(item[0])*scale), box.Min.Y+int(24*scale)
				desktopCall(desktopUser, "SetCursorPos", uintptr(x), uintptr(y))
				time.Sleep(80 * time.Millisecond)
				got, e := window.message(0x84, 0, hit(x, y))
				if e != nil {
					return e
				}
				if got != uintptr(item[1]) {
					return fmt.Errorf("native caption hit area differs")
				}
			}
			x, y := box.Min.X+int(24*scale), box.Min.Y+int(24*scale)
			desktopCall(desktopUser, "SetCursorPos", uintptr(x), uintptr(y))
			time.Sleep(80 * time.Millisecond)
			got, e := window.message(0x84, 0, hit(x, y))
			if e != nil {
				return e
			}
			if got != 2 {
				return fmt.Errorf("native drag region missing")
			}
			desktopCall(desktopUser, "SetCursorPos", uintptr(box.Min.X+int(200*scale)), uintptr(box.Min.Y+int(120*scale)))
			if e = window.setSize(int32(560*scale), int32(340*scale)); e != nil {
				return e
			}
			time.Sleep(300 * time.Millisecond)
			if e = window.screenshot(imagePath("-compact"), true); e != nil {
				return e
			}
			if e = window.setSize(1120, 720); e != nil {
				return e
			}
			time.Sleep(300 * time.Millisecond)
			return window.activate()
		})
		recordCase("border-drag-resizes-window-on-all-four-edges", func() (caseErr error) {
			if e := window.setSize(1120, 720); e != nil {
				return e
			}
			time.Sleep(400 * time.Millisecond)
			trace, e := startDesktopDragTrace(window)
			if e != nil {
				return e
			}
			defer func() {
				events := trace.finish()
				resizeDiagnostics["method"] = "atomic SendInput absolute move+button; bounded owned capture handshake; exact cursor and directional frame delta checks"
				if caseErr != nil {
					name := strings.TrimSuffix(output, filepath.Ext(output)) + "-resize-input.json"
					resizeDiagnostics["failure_trace"] = name
					if e := writeJSON(name, record{"window": window.handle, "pid": window.pid, "events": events}); e != nil {
						resizeDiagnostics["trace_write_error"] = e.Error()
					}
				}
			}()
			confirmed := []record{}
			for _, edge := range []struct {
				name        string
				hit, dx, dy int
			}{{"right", 11, -90, 0}, {"bottom", 15, 0, -70}, {"left", 10, 90, 0}, {"top", 12, 0, 70}} {
				box, e := window.bounds(false)
				if e != nil {
					return e
				}
				x, y := box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/2
				switch edge.name {
				case "right":
					x = box.Max.X - 1
				case "left":
					x = box.Min.X
				case "top":
					y = box.Min.Y
				default:
					y = box.Max.Y - 1
				}
				hit, e := window.message(0x84, 0, uintptr(uint32(uint16(x))|uint32(uint16(y))<<16))
				if e != nil {
					return e
				}
				if hit != uintptr(edge.hit) {
					return fmt.Errorf("%s border is not a resize handle", edge.name)
				}
				trace.sample("edge-begin", record{"edge": edge.name, "target": []int{x, y}, "hit": hit, "delta": []int{edge.dx, edge.dy}})
				before, e := window.bounds(true)
				if e != nil {
					return e
				}
				if e = window.dragPointer(trace, edge.name, x, y, edge.dx, edge.dy); e != nil {
					return e
				}
				after, e := window.bounds(true)
				if e != nil {
					return e
				}
				if before.Size() == after.Size() {
					return fmt.Errorf("dragging %s did not resize window", edge.name)
				}
				expected := before
				switch edge.name {
				case "right":
					expected.Max.X += edge.dx
				case "left":
					expected.Min.X += edge.dx
				case "bottom":
					expected.Max.Y += edge.dy
				case "top":
					expected.Min.Y += edge.dy
				}
				if after != expected {
					return fmt.Errorf("dragging %s resized to %v instead of requested %v", edge.name, after, expected)
				}
				confirmed = append(confirmed, record{"edge": edge.name, "before": before, "after": after, "pointer_delta": []int{edge.dx, edge.dy}})
				resizeDiagnostics["edges"] = confirmed
				if e = window.setSize(1120, 720); e != nil {
					return e
				}
				time.Sleep(400 * time.Millisecond)
			}
			return nil
		})
		recordCase("actual-gpui-gpu-window-chinese-and-color-screenshot", func() error {
			p := filepath.Join(root, "render.txt")
			if e := window.command(`printf '\033[2J\033[HGit-Terminal 中文 / UTF-8\nCOPY_MARKER_GIT_TERMINAL\n'; printf ready > ` + desktopQuote(p)); e != nil {
				return e
			}
			if _, e := desktopWaitFile(p, "ready", true); e != nil {
				return e
			}
			time.Sleep(400 * time.Millisecond)
			img, e := window.capture(false)
			if e != nil {
				return e
			}
			colors := map[uint32]bool{}
			for y := 0; y < img.Bounds().Dy() && len(colors) <= 20; y++ {
				for x := 0; x < img.Bounds().Dx() && len(colors) <= 20; x++ {
					p := img.RGBAAt(x, y)
					colors[uint32(p.R)<<16|uint32(p.G)<<8|uint32(p.B)] = true
				}
			}
			if len(colors) <= 20 {
				return fmt.Errorf("real GPUI image lacks rendered colors")
			}
			return window.screenshot(imagePath(""), false)
		})
		recordCase("native-mouse-selection-and-copy", func() error {
			if !clipboardSaved {
				return fmt.Errorf("clipboard snapshot is required before copy")
			}
			img, e := window.capture(false)
			if e != nil {
				return e
			}
			box, e := window.bounds(false)
			if e != nil {
				return e
			}
			scale := float64(desktopCall(desktopUser, "GetDpiForWindow", window.handle)) / 96
			background := img.RGBAAt(img.Bounds().Dx()-3, img.Bounds().Dy()/2)
			top := -1
			for y := 10; y < min(200, img.Bounds().Dy()/2); y++ {
				same := true
				for offset := 0; offset < 5; offset++ {
					same = same && img.RGBAAt(img.Bounds().Dx()-3, y+offset) == background
				}
				if same {
					top = y
					break
				}
			}
			if top < 0 {
				return fmt.Errorf("terminal content area missing")
			}
			bands := [][2]int{}
			active := -1
			for y := top + 8; y < min(top+160, img.Bounds().Dy()-45); y++ {
				lit := false
				for x := 12; x < min(350, img.Bounds().Dx()-20); x++ {
					p := img.RGBAAt(x, y)
					lit = lit || max(p.R, p.G, p.B) > 160
				}
				if lit && active < 0 {
					active = y
				}
				if !lit && active >= 0 {
					bands = append(bands, [2]int{active, y})
					active = -1
				}
			}
			targetY := float64(top) + 43*scale
			if len(bands) > 1 {
				targetY = float64(bands[1][0]+bands[1][1]) / 2
			}
			for _, dy := range []int{0, -3, 3} {
				if e = window.focused(); e != nil {
					return e
				}
				desktopCall(desktopUser, "SetCursorPos", uintptr(box.Min.X+int(14*scale)), uintptr(box.Min.Y+int(targetY+float64(dy)*scale)))
				if e = window.send([]desktopInput{desktopMouse(2, 0)}); e != nil {
					return e
				}
				time.Sleep(100 * time.Millisecond)
				desktopCall(desktopUser, "SetCursorPos", uintptr(box.Min.X+int(330*scale)), uintptr(box.Min.Y+int(targetY+float64(dy)*scale)))
				time.Sleep(150 * time.Millisecond)
				if e = window.send([]desktopInput{desktopMouse(4, 0)}); e != nil {
					return e
				}
				if e = window.shortcut(0x11, 0x10, 0x43); e != nil {
					return e
				}
				time.Sleep(150 * time.Millisecond)
				text, e := desktopClipboardText()
				if e != nil {
					return e
				}
				if strings.Contains(text, "COPY_MARKER_GIT_TERMINAL") {
					fixtureClipboard = text
					return nil
				}
			}
			return fmt.Errorf("native mouse selection did not copy marker")
		})
		recordCase("native-shift-page-and-mouse-wheel-scrollback", func() error {
			p := filepath.Join(root, "history.txt")
			if e := window.command(`printf 'history-%s\n' {1..180}; printf ready > ` + desktopQuote(p)); e != nil {
				return e
			}
			if _, e := desktopWaitFile(p, "ready", true); e != nil {
				return e
			}
			time.Sleep(300 * time.Millisecond)
			img, e := window.capture(false)
			if e != nil {
				return e
			}
			before := desktopCropHash(img)
			if e = window.shortcut(0x10, 0x21); e != nil {
				return e
			}
			time.Sleep(250 * time.Millisecond)
			img, e = window.capture(false)
			if e != nil {
				return e
			}
			if before == desktopCropHash(img) {
				return fmt.Errorf("Shift-PageUp did not change scrollback")
			}
			if e = window.shortcut(0x10, 0x22); e != nil {
				return e
			}
			time.Sleep(200 * time.Millisecond)
			box, e := window.bounds(false)
			if e != nil {
				return e
			}
			desktopCall(desktopUser, "SetCursorPos", uintptr(box.Min.X+150), uintptr(box.Min.Y+250))
			if e = window.send([]desktopInput{desktopMouse(0x800, 360)}); e != nil {
				return e
			}
			time.Sleep(250 * time.Millisecond)
			img, e = window.capture(false)
			if e != nil {
				return e
			}
			if before == desktopCropHash(img) {
				return fmt.Errorf("mouse wheel did not change scrollback")
			}
			return nil
		})
		recordCase("tab-first-design-default-bash-live-command-highlighting", func() error {
			if e := window.shortcut(0x11, 0x54); e != nil {
				return e
			}
			time.Sleep(1500 * time.Millisecond)
			if e := window.command(`clear; "$GIT_NEXT_EXECUTABLE" --version`); e != nil {
				return e
			}
			time.Sleep(500 * time.Millisecond)
			if e := window.text(`git log --oneline --all "中文路径"`); e != nil {
				return e
			}
			time.Sleep(500 * time.Millisecond)
			if e := window.screenshot(imagePath("-tabs-highlight"), true); e != nil {
				return e
			}
			if e := window.shortcut(0x11, 0x55); e != nil {
				return e
			}
			return window.shortcut(0x11, 0x57)
		})
		return nil
	}()
	if mainErr != nil {
		results = append(results, record{"case": "owned-native-window-startup", "status": "fail", "reason": mainErr.Error()})
	}
	cleanupMain()
	profileRun := func(profile, command string, timeout int, want int) error {
		p, e := startDesktop(binary, []string{"--shell", profile, "--no-profile", "--git", git, "--cwd", repo, "--command", command}, env)
		if e != nil {
			return e
		}
		defer p.close()
		code, e := p.wait(timeout)
		if e != nil {
			return e
		}
		if code != want {
			return fmt.Errorf("%s explicit command exit=%d", profile, code)
		}
		return nil
	}
	recordCase("owned-bash-and-native-cmd-real-gui-pty-exit", func() error {
		for _, profile := range []string{"powershell", "bash", "cmd"} {
			path := filepath.Join(root, profile+".txt")
			command := ""
			switch profile {
			case "powershell":
				command = "[IO.File]::WriteAllText('" + filepath.ToSlash(path) + "','powershell-ok')"
			case "bash":
				command = "printf 'bash-ok' > " + desktopQuote(path)
			case "cmd":
				command = `echo cmd-ok>"` + path + `"`
			}
			if e := profileRun(profile, command, 20, 0); e != nil {
				return e
			}
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if strings.TrimSpace(string(b)) != profile+"-ok" {
				return fmt.Errorf("%s GUI PTY output differs", profile)
			}
		}
		return nil
	})
	recordCase("explicit-command-exit-status-propagation", func() error { return profileRun("cmd", "exit 7", 20, 7) })
	recordCase("bounded-output-drain-and-command-exit-without-ui-deadlock", func() error { return profileRun("powershell", "[Console]::Write(('x' * 1048576)); exit 9", 30, 9) })
	status := "pass"
	for _, item := range results {
		if item["status"] != "pass" {
			status = "fail"
		}
	}
	binaryHash, e := digest(binary)
	if e != nil {
		return e
	}
	gitHash, e := digest(git)
	if e != nil {
		return e
	}
	harness, e := digest(a.path("tools/devtool/desktop_windows.go"))
	if e != nil {
		return e
	}
	clipboardHash, e := digest(a.path("tools/devtool/clipboard_windows.go"))
	if e != nil {
		return e
	}
	harnesses := record{}
	for _, name := range []string{"desktop.go", "desktop_windows.go", "desktop_drag_windows.go", "clipboard_windows.go", "desktop_lease_windows.go", "process.go", "process_windows.go", "files.go", "main.go", "go.mod", "go.sum"} {
		path := "tools/devtool/" + name
		h, e := digest(a.path(path))
		if e != nil {
			return e
		}
		harnesses[path] = h
	}
	scope := "all"
	if o.get("only-case") != "" {
		scope = "selected"
	}
	report := record{"status": status, "scope": scope, "paste_key": pasteKey, "ctrl_shift_v_registered_elsewhere": hotkeyConflict, "platform": "windows-" + strings.ReplaceAll(runtime.GOARCH, "amd64", "x64"), "renderer": "gpui-fast", "input_delivery": "owned HWND WM_CHAR UTF-16 commits; SendInput keyboard shortcuts", "binary_sha256": binaryHash, "git_sha256": gitHash, "harness_sha256": harness, "clipboard_harness_sha256": clipboardHash, "harness_language": "go", "case_set_version": 1, "harnesses": harnesses, "seconds": roundedSeconds(time.Since(start)), "personal_profile_accessed": false, "terminal_contents_logged": false, "results": results}
	report["harness_set_version"] = 3
	if len(resizeDiagnostics) > 0 {
		report["resize_input"] = resizeDiagnostics
	}
	if e = writeJSON(output, report); e != nil {
		return e
	}
	if e = a.json(report); e != nil {
		return e
	}
	if status != "pass" {
		return fmt.Errorf("native terminal desktop acceptance failed")
	}
	return nil
}
