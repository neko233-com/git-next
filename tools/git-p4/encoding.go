// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/transform"
)

func lookupEncoding(name string) (encoding.Encoding, error) {
	name = strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	switch name {
	case "utf8", "utf-8":
		name = "UTF-8"
	case "latin1", "latin-1", "iso8859-1":
		name = "ISO-8859-1"
	case "ascii", "us-ascii":
		name = "US-ASCII"
	default:
		if parts := regexp.MustCompile(`^cp([0-9]+)$`).FindStringSubmatch(name); parts != nil {
			if strings.HasPrefix(parts[1], "125") {
				name = "windows-" + parts[1]
			} else {
				name = "IBM" + parts[1]
			}
		}
	}
	value, e := ianaindex.IANA.Encoding(name)
	if e != nil || value == nil {
		return nil, fmt.Errorf("unsupported text encoding %s", name)
	}
	return value, nil
}
func decodeEncoding(raw, name string, replace bool) (string, error) {
	decoder, e := lookupEncoding(name)
	if e != nil {
		return "", e
	}
	out, _, e := transform.String(decoder.NewDecoder(), raw)
	if e != nil {
		return "", e
	}
	if !replace && !utf8.ValidString(raw) && strings.ContainsRune(out, '�') {
		return "", fmt.Errorf("input contains undefined %s bytes", name)
	}
	return out, nil
}
func escapeMetadata(raw string) string {
	var b strings.Builder
	for _, c := range []byte(raw) {
		if c > 127 {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}
