// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/windows"
	"time"
	"unicode/utf16"
	"unsafe"
)

var desktopUser = windows.NewLazySystemDLL("user32.dll")
var desktopKernel = windows.NewLazySystemDLL("kernel32.dll")
var desktopGDI = windows.NewLazySystemDLL("gdi32.dll")

//go:uintptrescapes
func desktopCall(dll *windows.LazyDLL, name string, args ...uintptr) uintptr {
	r, _, _ := dll.NewProc(name).Call(args...)
	return r
}

//go:uintptrescapes
func desktopCallError(dll *windows.LazyDLL, name string, args ...uintptr) (uintptr, error) {
	r, _, e := dll.NewProc(name).Call(args...)
	return r, e
}
func clipboardOpen(owner uintptr) error {
	end := time.Now().Add(2 * time.Second)
	for time.Now().Before(end) {
		if desktopCall(desktopUser, "OpenClipboard", owner) != 0 {
			return nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	return fmt.Errorf("clipboard remained busy; contents were preserved")
}
func clipboardAllocate(data []byte) (uintptr, error) {
	size := len(data)
	if size == 0 {
		size = 1
	}
	h := desktopCall(desktopKernel, "GlobalAlloc", 0x42, uintptr(size))
	if h == 0 {
		return 0, fmt.Errorf("clipboard allocation failed")
	}
	p := desktopCall(desktopKernel, "GlobalLock", h)
	if p == 0 {
		desktopCall(desktopKernel, "GlobalFree", h)
		return 0, fmt.Errorf("clipboard lock failed")
	}
	var written uintptr
	if len(data) > 0 {
		e := windows.WriteProcessMemory(windows.CurrentProcess(), p, &data[0], uintptr(len(data)), &written)
		if e != nil || written != uintptr(len(data)) {
			desktopCall(desktopKernel, "GlobalUnlock", h)
			desktopCall(desktopKernel, "GlobalFree", h)
			return 0, fmt.Errorf("clipboard memory write failed")
		}
	}
	desktopCall(desktopKernel, "GlobalUnlock", h)
	return h, nil
}
func clipboardMemory(address, size uintptr) ([]byte, error) {
	if size == 0 || size > 256<<20 {
		return nil, fmt.Errorf("clipboard memory capacity invalid")
	}
	data := make([]byte, int(size))
	var read uintptr
	if e := windows.ReadProcessMemory(windows.CurrentProcess(), address, &data[0], size, &read); e != nil || read != size {
		return nil, fmt.Errorf("clipboard memory read failed")
	}
	return data, nil
}

type clipboardFormat struct {
	format     uint32
	kind       string
	data       []byte
	handle     uintptr
	mode, x, y int32
}
type clipboardSnapshot struct{ formats []clipboardFormat }
type clipboardMetaPicture struct {
	mode, x, y int32
	pad        uint32
	metafile   uintptr
}

func (c *clipboardSnapshot) close() {
	for _, f := range c.formats {
		switch f.kind {
		case "bitmap":
			desktopCall(desktopGDI, "DeleteObject", f.handle)
		case "enhanced":
			desktopCall(desktopGDI, "DeleteEnhMetaFile", f.handle)
		case "metafile":
			desktopCall(desktopGDI, "DeleteMetaFile", f.handle)
		}
	}
	c.formats = nil
}
func (c *clipboardSnapshot) capture() (e error) {
	if e = clipboardOpen(0); e != nil {
		return e
	}
	defer desktopCall(desktopUser, "CloseClipboard")
	defer func() {
		if e != nil {
			c.close()
		}
	}()
	format := uint32(0)
	total := uintptr(0)
	for {
		desktopCall(desktopKernel, "SetLastError", 0)
		value, last := desktopCallError(desktopUser, "EnumClipboardFormats", uintptr(format))
		format = uint32(value)
		if format == 0 {
			if last != nil && last != windows.ERROR_SUCCESS {
				return fmt.Errorf("clipboard enumeration failed before mutation: %w", last)
			}
			break
		}
		if len(c.formats) >= 256 {
			return fmt.Errorf("clipboard format limit; content was preserved")
		}
		handle := desktopCall(desktopUser, "GetClipboardData", uintptr(format))
		if handle == 0 {
			return fmt.Errorf("clipboard format unavailable; content was preserved")
		}
		f := clipboardFormat{format: format}
		switch format {
		case 2, 0x82:
			f.kind = "bitmap"
			f.handle = desktopCall(desktopUser, "CopyImage", handle, 0, 0, 0, 0x2000)
			if f.handle == 0 {
				return fmt.Errorf("clipboard bitmap copy failed")
			}
		case 14, 0x8e:
			f.kind = "enhanced"
			f.handle = desktopCall(desktopGDI, "CopyEnhMetaFileW", handle, 0)
			if f.handle == 0 {
				return fmt.Errorf("clipboard metafile copy failed")
			}
		case 9:
			f.kind = "palette"
			count := desktopCall(desktopGDI, "GetPaletteEntries", handle, 0, 0, 0)
			if count == 0 || count > 65535 {
				return fmt.Errorf("clipboard palette size unsupported")
			}
			f.data = make([]byte, 4+count*4)
			binary.LittleEndian.PutUint16(f.data, 0x300)
			binary.LittleEndian.PutUint16(f.data[2:], uint16(count))
			if desktopCall(desktopGDI, "GetPaletteEntries", handle, 0, count, uintptr(unsafe.Pointer(&f.data[4]))) != count {
				return fmt.Errorf("clipboard palette copy failed")
			}
		default:
			size := desktopCall(desktopKernel, "GlobalSize", handle)
			total += size
			if size == 0 || size > 256<<20 || total > 256<<20 {
				return fmt.Errorf("unsupported clipboard handle capacity; content was preserved")
			}
			p := desktopCall(desktopKernel, "GlobalLock", handle)
			if p == 0 {
				return fmt.Errorf("clipboard format lock failed")
			}
			data, readErr := clipboardMemory(p, size)
			if readErr != nil {
				desktopCall(desktopKernel, "GlobalUnlock", handle)
				return readErr
			}
			if format == 3 || format == 0x83 {
				if len(data) < 24 {
					desktopCall(desktopKernel, "GlobalUnlock", handle)
					return fmt.Errorf("clipboard metafile picture is truncated")
				}
				f.kind = "metafile"
				f.mode, f.x, f.y = int32(binary.LittleEndian.Uint32(data)), int32(binary.LittleEndian.Uint32(data[4:])), int32(binary.LittleEndian.Uint32(data[8:]))
				f.handle = desktopCall(desktopGDI, "CopyMetaFileW", uintptr(binary.LittleEndian.Uint64(data[16:])), 0)
			} else {
				f.kind = "memory"
				f.data = data
			}
			desktopCall(desktopKernel, "GlobalUnlock", handle)
			if f.kind == "metafile" && f.handle == 0 {
				return fmt.Errorf("clipboard legacy metafile copy failed")
			}
		}
		c.formats = append(c.formats, f)
	}
	return nil
}
func freeClipboardFormat(f clipboardFormat, h uintptr) {
	switch f.kind {
	case "bitmap", "palette":
		desktopCall(desktopGDI, "DeleteObject", h)
	case "enhanced":
		desktopCall(desktopGDI, "DeleteEnhMetaFile", h)
	case "metafile":
		p := desktopCall(desktopKernel, "GlobalLock", h)
		if p != 0 {
			if data, e := clipboardMemory(p, 24); e == nil {
				desktopCall(desktopGDI, "DeleteMetaFile", uintptr(binary.LittleEndian.Uint64(data[16:])))
			}
			desktopCall(desktopKernel, "GlobalUnlock", h)
		}
		desktopCall(desktopKernel, "GlobalFree", h)
	default:
		desktopCall(desktopKernel, "GlobalFree", h)
	}
}
func (c *clipboardSnapshot) restore(owner uintptr) error {
	if e := clipboardOpen(owner); e != nil {
		return e
	}
	defer desktopCall(desktopUser, "CloseClipboard")
	type prepared struct {
		f clipboardFormat
		h uintptr
	}
	items := []prepared{}
	defer func() {
		for _, item := range items {
			if item.h != 0 {
				freeClipboardFormat(item.f, item.h)
			}
		}
	}()
	for _, f := range c.formats {
		h := uintptr(0)
		var e error
		switch f.kind {
		case "memory":
			h, e = clipboardAllocate(f.data)
		case "bitmap":
			h = desktopCall(desktopUser, "CopyImage", f.handle, 0, 0, 0, 0x2000)
		case "enhanced":
			h = desktopCall(desktopGDI, "CopyEnhMetaFileW", f.handle, 0)
		case "palette":
			h = desktopCall(desktopGDI, "CreatePalette", uintptr(unsafe.Pointer(&f.data[0])))
		case "metafile":
			copied := desktopCall(desktopGDI, "CopyMetaFileW", f.handle, 0)
			if copied == 0 {
				return fmt.Errorf("clipboard metafile restore copy failed")
			}
			picture := clipboardMetaPicture{mode: f.mode, x: f.x, y: f.y, metafile: copied}
			h, e = clipboardAllocate(unsafe.Slice((*byte)(unsafe.Pointer(&picture)), int(unsafe.Sizeof(picture))))
			if e != nil {
				desktopCall(desktopGDI, "DeleteMetaFile", copied)
			}
		}
		if e != nil {
			return e
		}
		if h == 0 {
			return fmt.Errorf("clipboard restore allocation failed before mutation")
		}
		items = append(items, prepared{f, h})
	}
	if desktopCall(desktopUser, "EmptyClipboard") == 0 {
		return fmt.Errorf("clipboard restore failed")
	}
	for i, item := range items {
		if desktopCall(desktopUser, "SetClipboardData", uintptr(item.f.format), item.h) == 0 {
			return fmt.Errorf("clipboard format restoration failed")
		}
		items[i].h = 0
	}
	return nil
}
func desktopClipboardText() (string, error) {
	if e := clipboardOpen(0); e != nil {
		return "", e
	}
	defer desktopCall(desktopUser, "CloseClipboard")
	h := desktopCall(desktopUser, "GetClipboardData", 13)
	if h == 0 {
		return "", nil
	}
	size := desktopCall(desktopKernel, "GlobalSize", h)
	if size > 256<<20 {
		return "", fmt.Errorf("clipboard text exceeds limit")
	}
	p := desktopCall(desktopKernel, "GlobalLock", h)
	if p == 0 {
		return "", fmt.Errorf("clipboard text lock failed")
	}
	defer desktopCall(desktopKernel, "GlobalUnlock", h)
	data, e := clipboardMemory(p, size)
	if e != nil {
		return "", e
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	n := 0
	for n < len(units) && units[n] != 0 {
		n++
	}
	return string(utf16.Decode(units[:n])), nil
}
func desktopSetClipboard(owner uintptr, text string) error {
	data := utf16.Encode([]rune(text))
	data = append(data, 0)
	bytes := make([]byte, len(data)*2)
	for i, u := range data {
		binary.LittleEndian.PutUint16(bytes[i*2:], u)
	}
	h, e := clipboardAllocate(bytes)
	if e != nil {
		return e
	}
	transferred := false
	defer func() {
		if !transferred {
			desktopCall(desktopKernel, "GlobalFree", h)
		}
	}()
	if e = clipboardOpen(owner); e != nil {
		return e
	}
	defer desktopCall(desktopUser, "CloseClipboard")
	if desktopCall(desktopUser, "EmptyClipboard") == 0 {
		return fmt.Errorf("clipboard fixture mutation refused")
	}
	if desktopCall(desktopUser, "SetClipboardData", 13, h) == 0 {
		return fmt.Errorf("clipboard fixture allocation refused")
	}
	transferred = true
	return nil
}
