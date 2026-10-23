// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"encoding/binary"
	"fmt"
	"image"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func desktopAbsoluteMouse(x, y int, flags uint32, screen image.Rectangle) (desktopInput, error) {
	if !image.Pt(x, y).In(screen) || screen.Dx() > 65536 || screen.Dy() > 65536 {
		return desktopInput{}, fmt.Errorf("mouse target is outside the representable virtual desktop")
	}
	input := desktopMouse(flags|0xe001, 0)
	// Target the pixel center, including monitors left/above the primary display.
	binary.LittleEndian.PutUint32(input.data[0:], uint32((int64(2*(x-screen.Min.X))+1)*65536/int64(2*screen.Dx())))
	binary.LittleEndian.PutUint32(input.data[4:], uint32((int64(2*(y-screen.Min.Y))+1)*65536/int64(2*screen.Dy())))
	binary.LittleEndian.PutUint64(input.data[24:], 0x2330a11ce)
	return input, nil
}

func (w desktopWindow) mouseAt(x, y int, flags uint32) error {
	metric := func(index uintptr) int { return int(int32(desktopCall(desktopUser, "GetSystemMetrics", index))) }
	x0, y0 := metric(76), metric(77)
	input, err := desktopAbsoluteMouse(x, y, flags, image.Rect(x0, y0, x0+metric(78), y0+metric(79)))
	if err != nil {
		return err
	}
	return w.send([]desktopInput{input})
}

func (w desktopWindow) guiThreadInfo() (desktopGUIThreadInfo, error) {
	thread := desktopCall(desktopUser, "GetWindowThreadProcessId", w.handle, 0)
	info := desktopGUIThreadInfo{}
	info.size = uint32(unsafe.Sizeof(info))
	if desktopCall(desktopUser, "GetGUIThreadInfo", thread, uintptr(unsafe.Pointer(&info))) == 0 {
		return info, fmt.Errorf("owned GUI capture state unavailable")
	}
	return info, nil
}

func (w desktopWindow) dragPointer(trace *desktopDragTrace, edge string, x, y, dx, dy int) error {
	if desktopCall(desktopUser, "GetAsyncKeyState", 1)&0x8000 != 0 {
		return fmt.Errorf("drag input interrupted: left button was already held")
	}
	trace.sample("before-button-down", record{"edge": edge, "target": []int{x, y}, "send_input_flags": 0xe003})
	if err := w.mouseAt(x, y, 2); err != nil {
		return err
	}
	held, lastX, lastY := true, x, y
	defer func() {
		if held {
			_ = w.mouseAt(lastX, lastY, 4)
		}
	}()
	if err := desktopWait(2, func() (bool, error) {
		info, err := w.guiThreadInfo()
		return info.capture == w.handle, err
	}); err != nil {
		trace.sample("capture-handshake-failed", record{"edge": edge})
		return fmt.Errorf("drag never acquired the owned mouse capture: %w", err)
	}
	check := func(x, y int) error {
		point := desktopPoint{}
		if desktopCall(desktopUser, "GetCursorPos", uintptr(unsafe.Pointer(&point))) == 0 {
			return fmt.Errorf("drag cursor unavailable")
		}
		if int(point.x) != x || int(point.y) != y {
			return fmt.Errorf("drag input interrupted: cursor displaced from (%d,%d) to (%d,%d); source unknown", x, y, point.x, point.y)
		}
		info, err := w.guiThreadInfo()
		if err != nil {
			return err
		}
		if info.capture != w.handle || desktopCall(desktopUser, "GetAsyncKeyState", 1)&0x8000 == 0 || desktopCall(desktopUser, "GetForegroundWindow") != w.handle {
			return fmt.Errorf("drag input interrupted: owned capture, button or focus changed")
		}
		return nil
	}
	trace.sample("after-button-down", record{"edge": edge})
	if err := check(x, y); err != nil {
		return err
	}
	for step := 1; step <= 8; step++ {
		lastX, lastY = x+dx*step/8, y+dy*step/8
		if err := w.mouseAt(lastX, lastY, 0); err != nil {
			return err
		}
		time.Sleep(40 * time.Millisecond)
		trace.sample("pointer-move", record{"edge": edge, "step": step, "target": []int{lastX, lastY}})
		if err := check(lastX, lastY); err != nil {
			return err
		}
	}
	trace.sample("before-button-up", record{"edge": edge, "target": []int{lastX, lastY}, "send_input_flags": 0xe005})
	if err := w.mouseAt(lastX, lastY, 4); err != nil {
		return err
	}
	held = false
	time.Sleep(350 * time.Millisecond)
	trace.sample("after-button-up", record{"edge": edge})
	return nil
}

type desktopGUIThreadInfo struct {
	size, flags                                        uint32
	active, focus, capture, menuOwner, moveSize, caret uintptr
	caretRect                                          desktopRect
}

type desktopMessage struct {
	window         uintptr
	message        uint32
	wparam, lparam uintptr
	timestamp      uint32
	point          desktopPoint
	private        uint32
}

type desktopDragTrace struct {
	window desktopWindow
	mu     sync.Mutex
	events []record
	stop   chan struct{}
	done   chan struct{}
}

func (t *desktopDragTrace) sample(phase string, fields record) {
	if fields == nil {
		fields = record{}
	}
	fields["phase"] = phase
	fields["observed_tick_ms"] = desktopCall(desktopKernel, "GetTickCount64")
	point := desktopPoint{}
	if desktopCall(desktopUser, "GetCursorPos", uintptr(unsafe.Pointer(&point))) != 0 {
		fields["cursor"] = []int32{point.x, point.y}
		packed := uintptr(uint64(uint32(point.x)) | uint64(uint32(point.y))<<32)
		under := desktopCall(desktopUser, "WindowFromPoint", packed)
		fields["window_at_cursor"] = under
		fields["root_at_cursor"] = desktopCall(desktopUser, "GetAncestor", under, 2)
	}
	fields["left_button_down"] = desktopCall(desktopUser, "GetAsyncKeyState", 1)&0x8000 != 0
	fields["foreground"] = desktopCall(desktopUser, "GetForegroundWindow")
	thread := desktopCall(desktopUser, "GetWindowThreadProcessId", t.window.handle, 0)
	info := desktopGUIThreadInfo{}
	info.size = uint32(unsafe.Sizeof(info))
	if desktopCall(desktopUser, "GetGUIThreadInfo", thread, uintptr(unsafe.Pointer(&info))) != 0 {
		fields["capture"] = info.capture
		fields["move_size"] = info.moveSize
		fields["gui_flags"] = info.flags
	}
	if box, err := t.window.bounds(true); err == nil {
		fields["frame"] = []int{box.Min.X, box.Min.Y, box.Max.X, box.Max.Y}
	}
	t.mu.Lock()
	t.events = append(t.events, fields)
	t.mu.Unlock()
}

func startDesktopDragTrace(window desktopWindow) (*desktopDragTrace, error) {
	trace := &desktopDragTrace{window: window, stop: make(chan struct{}), done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(trace.done)
		callback := windows.NewCallback(func(hook, event, hwnd, object, child, thread, stamp uintptr) uintptr {
			if hwnd == window.handle {
				names := map[uint32]string{8: "EVENT_SYSTEM_CAPTURESTART", 9: "EVENT_SYSTEM_CAPTUREEND", 10: "EVENT_SYSTEM_MOVESIZESTART", 11: "EVENT_SYSTEM_MOVESIZEEND"}
				trace.sample(names[uint32(event)], record{"event_tick_ms": uint32(stamp), "event_thread": uint32(thread)})
			}
			return 0
		})
		hook := desktopCall(desktopUser, "SetWinEventHook", 8, 11, 0, callback, uintptr(window.pid), 0, 0)
		if hook == 0 {
			ready <- fmt.Errorf("owned resize event hook could not be installed")
			return
		}
		defer desktopCall(desktopUser, "UnhookWinEvent", hook)
		ready <- nil
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			message := desktopMessage{}
			for desktopCall(desktopUser, "PeekMessageW", uintptr(unsafe.Pointer(&message)), 0, 0, 0, 1) != 0 {
				desktopCall(desktopUser, "TranslateMessage", uintptr(unsafe.Pointer(&message)))
				desktopCall(desktopUser, "DispatchMessageW", uintptr(unsafe.Pointer(&message)))
			}
			select {
			case <-trace.stop:
				return
			case <-ticker.C:
			}
		}
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return trace, nil
}

func (t *desktopDragTrace) finish() []record {
	close(t.stop)
	<-t.done
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]record{}, t.events...)
}
