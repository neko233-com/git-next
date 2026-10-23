// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"encoding/binary"
	"image"
	"testing"
)

func TestDesktopAbsoluteMouseCombinesCoordinatesAndButton(t *testing.T) {
	screen := image.Rect(-3840, -1080, 3840, 2160)
	for _, point := range []image.Point{screen.Min, image.Pt(-100, -100), image.Pt(0, 0), screen.Max.Sub(image.Pt(1, 1))} {
		for _, button := range []uint32{0, 2, 4} {
			input, err := desktopAbsoluteMouse(point.X, point.Y, button, screen)
			if err != nil {
				t.Fatal(err)
			}
			if input.kind != 0 || binary.LittleEndian.Uint32(input.data[12:]) != 0xe001|button {
				t.Fatal("absolute move and button were not combined in one mouse event")
			}
			x := int(binary.LittleEndian.Uint32(input.data[0:]))*screen.Dx()/65536 + screen.Min.X
			y := int(binary.LittleEndian.Uint32(input.data[4:]))*screen.Dy()/65536 + screen.Min.Y
			if image.Pt(x, y) != point {
				t.Fatalf("absolute coordinate reached (%d,%d), want %v", x, y, point)
			}
			if binary.LittleEndian.Uint64(input.data[24:]) == 0 {
				t.Fatal("owned mouse input lacks its diagnostic tag")
			}
		}
	}
}

func TestDesktopAbsoluteMouseRejectsUnrepresentableTarget(t *testing.T) {
	for _, screen := range []image.Rectangle{image.Rect(0, 0, 0, 0), image.Rect(100, 100, 200, 200), image.Rect(0, 0, 70000, 200)} {
		if _, err := desktopAbsoluteMouse(0, 0, 2, screen); err == nil {
			t.Fatalf("unrepresentable target accepted: %v", screen)
		}
	}
}
