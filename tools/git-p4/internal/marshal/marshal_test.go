// Copyright 2026 git-next contributors. MIT license.
package marshal

import (
	"bytes"
	"encoding/hex"
	"io"
	"reflect"
	"testing"
)

func TestP4WireDictionaryAndBinaryData(t *testing.T) {
	input := map[string]any{"code": []byte("text"), "data": []byte{0, 255, 13, 10}, "severity": int64(3), "change": int64(4294967296)}
	var b bytes.Buffer
	if e := Encode(&b, input); e != nil {
		t.Fatal(e)
	}
	got, e := NewDecoder(&b).Decode()
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(got, input) {
		t.Fatalf("binary data changed: %#v", got)
	}
}
func TestPythonMarshalOracle(t *testing.T) {
	for _, wire := range []string{"7b7304000000636f646573040000007374617473060000006368616e67657302000000343230", "fbda04636f6465da0473746174da066368616e6765da02343230"} {
		b, e := hex.DecodeString(wire)
		if e != nil {
			t.Fatal(e)
		}
		v, e := NewDecoder(bytes.NewReader(b)).Decode()
		if e != nil {
			t.Fatal(e)
		}
		m := v.(map[string]any)
		text := func(v any) string {
			if b, ok := v.([]byte); ok {
				return string(b)
			}
			return v.(string)
		}
		if text(m["code"]) != "stat" || text(m["change"]) != "42" {
			t.Fatalf("oracle differs %#v", m)
		}
	}
}
func TestConcatenatedRecordsAndMalformedInput(t *testing.T) {
	var b bytes.Buffer
	for i := 0; i < 3; i++ {
		if e := Encode(&b, map[string]any{"number": int64(i)}); e != nil {
			t.Fatal(e)
		}
	}
	d := NewDecoder(&b)
	for i := 0; i < 3; i++ {
		v, e := d.Decode()
		if e != nil || v.(map[string]any)["number"] != int64(i) {
			t.Fatalf("stream record differs: %v %v", v, e)
		}
	}
	if _, e := d.Decode(); e != io.EOF {
		t.Fatalf("wanted EOF: %v", e)
	}
	for _, wire := range [][]byte{{'s', 255, 255, 255, 127}, {'r', 9, 0, 0, 0}, {'{', 'i', 1, 0, 0, 0, 'N', '0'}, {'l', 0, 0, 64, 0}} {
		if _, e := NewDecoder(bytes.NewReader(wire)).Decode(); e == nil {
			t.Fatalf("invalid record accepted %x", wire)
		}
	}
}
