// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

func init() {
	register("integrate-native-core", "Apply pinned patch chains and generate native identity/catalog", "source", integrateNativeCore)
	register("native-source-identity", "Read the frozen native source content identity", "source", func(a *app, o options) error {
		if e := o.require("source"); e != nil {
			return e
		}
		h, e := sourceIdentity(o.get("source"))
		if e != nil {
			return e
		}
		return a.json(record{"source_tree_sha256": h})
	})
}
func writeChanged(p string, data []byte) error {
	old, e := os.ReadFile(p)
	if e == nil && bytes.Equal(old, data) {
		return nil
	}
	return write(p, data, 0644)
}
func applyUnified(original, patch string) (string, error) {
	source := strings.SplitAfter(original, "\n")
	if source[len(source)-1] == "" {
		source = source[:len(source)-1]
	}
	lines := strings.SplitAfter(patch, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	pattern := regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@[^\n]*\n?$`)
	var output strings.Builder
	position := 0
	for i := 2; i < len(lines); {
		m := pattern.FindStringSubmatch(lines[i])
		if len(m) == 0 {
			return "", fmt.Errorf("unexpected patch hunk")
		}
		start, _ := strconv.Atoi(m[1])
		start--
		if start < position || start > len(source) {
			return "", fmt.Errorf("overlapping/out-of-range patch")
		}
		for _, line := range source[position:start] {
			output.WriteString(line)
		}
		position = start
		removed, added := 0, 0
		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], "@@ ") {
			line := lines[i]
			if len(line) == 0 || !strings.ContainsRune(" +-", rune(line[0])) {
				return "", fmt.Errorf("unexpected patch line")
			}
			if line[0] == ' ' || line[0] == '-' {
				if position >= len(source) || source[position] != line[1:] {
					return "", fmt.Errorf("patch context differs from pinned source")
				}
				position++
				removed++
			}
			if line[0] == ' ' || line[0] == '+' {
				output.WriteString(line[1:])
				added++
			}
			i++
		}
		wantRemoved, wantAdded := 1, 1
		if m[2] != "" {
			wantRemoved, _ = strconv.Atoi(m[2])
		}
		if m[4] != "" {
			wantAdded, _ = strconv.Atoi(m[4])
		}
		if removed != wantRemoved || added != wantAdded {
			return "", fmt.Errorf("patch hunk count mismatch")
		}
	}
	for _, line := range source[position:] {
		output.WriteString(line)
	}
	return output.String(), nil
}
func canonicalPythonJSON(v any) []byte {
	switch value := v.(type) {
	case map[string]string:
		m := record{}
		for k, v := range value {
			m[k] = v
		}
		return canonicalPythonJSON(m)
	case map[string]any:
		keys := make([]string, 0, len(value))
		for k := range value {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, string(canonicalPythonJSON(k))+": "+string(canonicalPythonJSON(value[k])))
		}
		return []byte("{" + strings.Join(parts, ", ") + "}")
	case []any:
		parts := make([]string, len(value))
		for i, v := range value {
			parts[i] = string(canonicalPythonJSON(v))
		}
		return []byte("[" + strings.Join(parts, ", ") + "]")
	default:
		if value, ok := v.(string); ok {
			return []byte(pythonASCIIQuote(value))
		}
		b, _ := json.Marshal(v)
		return b
	}
}
func pythonASCIIQuote(value string) string { return pythonJSONQuote(value, true) }
func pythonJSONQuote(value string, ascii bool) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 32 || ascii && r >= 127 {
				if r > 0xffff {
					hi, lo := utf16.EncodeRune(r)
					fmt.Fprintf(&out, `\u%04x\u%04x`, hi, lo)
				} else {
					fmt.Fprintf(&out, `\u%04x`, r)
				}
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

// The identity is an embedded wire string; preserve the original declaration order.
func orderedCompactJSON(data []byte) (string, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func() (string, error)
	value = func() (string, error) {
		t, e := d.Token()
		if e != nil {
			return "", e
		}
		switch v := t.(type) {
		case json.Delim:
			var parts []string
			if v == '{' {
				for d.More() {
					key, e := d.Token()
					if e != nil {
						return "", e
					}
					item, e := value()
					if e != nil {
						return "", e
					}
					parts = append(parts, pythonASCIIQuote(key.(string))+":"+item)
				}
				if _, e = d.Token(); e != nil {
					return "", e
				}
				return "{" + strings.Join(parts, ",") + "}", nil
			}
			if v == '[' {
				for d.More() {
					item, e := value()
					if e != nil {
						return "", e
					}
					parts = append(parts, item)
				}
				if _, e = d.Token(); e != nil {
					return "", e
				}
				return "[" + strings.Join(parts, ",") + "]", nil
			}
			return "", fmt.Errorf("unexpected JSON delimiter")
		case string:
			return pythonASCIIQuote(v), nil
		case json.Number:
			return v.String(), nil
		default:
			b, e := json.Marshal(v)
			return string(b), e
		}
	}
	out, e := value()
	if e != nil {
		return "", e
	}
	if _, e = d.Token(); e != io.EOF {
		return "", fmt.Errorf("trailing JSON content")
	}
	return out, nil
}
func sourceIdentity(source string) (string, error) {
	prepared, e := load(filepath.Join(source, "git-next-source-receipt.json"))
	if e != nil {
		return "", e
	}
	integrated, e := load(filepath.Join(source, "git-next-integration-receipt.json"))
	if e != nil {
		return "", e
	}
	names := map[string]bool{"git-next-catalog.h": true, "git-next-identity.h": true}
	for name := range object(prepared, "files") {
		names[name] = true
	}
	for name := range object(integrated, "overlay") {
		names[name] = true
	}
	values := map[string]string{}
	for name := range names {
		p, e := confined(source, name)
		if e != nil {
			return "", e
		}
		hash, e := digest(p)
		if e != nil {
			return "", e
		}
		values[name] = hash
	}
	return digestBytes(canonicalPythonJSON(values)), nil
}
func nativeCompatibility(a *app) (record, error) {
	m, e := load(a.path("packaging/native-compatibility.json"))
	if e != nil {
		return nil, e
	}
	_, boolOK := m["complete_git_parity"].(bool)
	if number(m, "schema_version") != 1 || stringValue(m, "product") != "git-next" || !boolOK || m["external_git_fallback"] != false || m["external_ssh_fallback"] != false {
		return nil, fmt.Errorf("invalid native compatibility declaration")
	}
	return m, nil
}
func poMessages(text string) (map[string]string, error) {
	messages := map[string]string{}
	entry := map[string]string{}
	field := ""
	fuzzy := false
	for _, line := range append(strings.Split(text, "\n"), "") {
		if strings.TrimSpace(line) == "" {
			_, hasContext := entry["msgctxt"]
			if entry["msgid"] != "" && !fuzzy && !hasContext {
				translated, hasSingular := entry["msgstr"]
				if !hasSingular {
					translated = entry["msgstr[0]"]
				}
				if translated != "" {
					messages[entry["msgid"]] = translated
				}
			}
			entry = map[string]string{}
			field = ""
			fuzzy = false
		} else if strings.HasPrefix(line, "#,") {
			for _, flag := range strings.Split(line, ",") {
				fuzzy = fuzzy || strings.TrimSpace(flag) == "fuzzy"
			}
		} else if strings.HasPrefix(line, "#") {
			continue
		} else if strings.HasPrefix(line, `"`) {
			if field == "" {
				return nil, fmt.Errorf("PO continuation without field")
			}
			value, e := strconv.Unquote(line)
			if e != nil {
				return nil, e
			}
			entry[field] += value
		} else {
			key, quoted, ok := strings.Cut(line, " ")
			if !ok {
				return nil, fmt.Errorf("invalid PO field")
			}
			field = key
			if key != "msgid" && key != "msgid_plural" && key != "msgstr" && key != "msgctxt" && !strings.HasPrefix(key, "msgstr[") {
				return nil, fmt.Errorf("unsupported PO field: %s", key)
			}
			value, e := strconv.Unquote(quoted)
			if e != nil {
				return nil, e
			}
			entry[field] = value
		}
	}
	return messages, nil
}
func cLiteral(value string) string {
	pattern := regexp.MustCompile(`<PRI[A-Za-z0-9_]+>`)
	positions := pattern.FindAllStringIndex(value, -1)
	offset := 0
	var parts []string
	for _, position := range positions {
		parts = append(parts, pythonJSONQuote(value[offset:position[0]], false), value[position[0]+1:position[1]-1])
		offset = position[1]
	}
	parts = append(parts, pythonJSONQuote(value[offset:], false))
	return strings.Join(parts, " ")
}
func generateCatalog(a *app, source string) (record, error) {
	p := filepath.Join(source, "po/zh_CN.po")
	text, e := read(p)
	if e != nil {
		return nil, e
	}
	messages, e := poMessages(text)
	if e != nil {
		return nil, e
	}
	upstream := len(messages)
	additionalPath := a.path("native-core/locale/zh_CN.json")
	additional, e := load(additionalPath)
	if e != nil {
		return nil, e
	}
	for key, value := range additional {
		translation, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("native translation must be a string")
		}
		messages[key] = translation
	}
	keys := make([]string, 0, len(messages))
	for key, value := range messages {
		if strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("NUL in translation")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pattern := regexp.MustCompile(`<PRI[A-Za-z0-9_]+>`)
	header := "/* Generated from pinned Git po/zh_CN.po. SPDX-License-Identifier: GPL-2.0-only */\n/* Contributor attribution and source remain in po/zh_CN.po and the source receipt. */\nstruct git_next_translation { const char *english; const char *chinese; };\n"
	portable := 0
	for _, table := range []struct {
		name     string
		portable bool
	}{{"git_next_catalog", false}, {"git_next_portable_catalog", true}} {
		header += "static const struct git_next_translation " + table.name + "[] = {\n"
		for _, key := range keys {
			if pattern.MatchString(key) != table.portable {
				continue
			}
			if table.portable {
				portable++
			}
			header += "\t{ " + cLiteral(key) + ", " + cLiteral(messages[key]) + " },\n"
		}
		header += "};\n"
	}
	if e = writeChanged(filepath.Join(source, "git-next-catalog.h"), []byte(header)); e != nil {
		return nil, e
	}
	sourceHash, e := digest(p)
	if e != nil {
		return nil, e
	}
	additionalHash, e := digest(additionalPath)
	if e != nil {
		return nil, e
	}
	return record{"source": "po/zh_CN.po", "source_sha256": sourceHash, "generated_sha256": digestBytes([]byte(header)), "translations": len(messages), "upstream_translations": upstream, "additional_source": "native-core/locale/zh_CN.json", "additional_source_sha256": additionalHash, "portable_formats": portable, "fuzzy_entries_excluded": true}, nil
}
func integrateNativeCore(a *app, o options) error {
	if e := o.require("source"); e != nil {
		return e
	}
	source, e := absolute(o.get("source"))
	if e != nil {
		return e
	}
	pin, e := load(a.path("packaging/native-core.lock.json"))
	if e != nil {
		return e
	}
	prepared, e := load(filepath.Join(source, "git-next-source-receipt.json"))
	if e != nil {
		return e
	}
	if prepared["commit"] != pin["commit"] || prepared["archive_sha256"] != pin["archive_sha256"] {
		return fmt.Errorf("source receipt differs from pin")
	}
	data, e := os.ReadFile(a.path("native-core/patches/series.json"))
	if e != nil {
		return e
	}
	var series []record
	if e = json.Unmarshal(data, &series); e != nil {
		return e
	}
	allowed := map[string]map[string]bool{}
	chains := map[string][]string{}
	for _, entry := range series {
		name := stringValue(entry, "path")
		before, after := stringValue(entry, "before_sha256"), stringValue(entry, "after_sha256")
		if allowed[name] == nil {
			allowed[name] = map[string]bool{}
			chains[name] = []string{before}
		}
		chain := chains[name]
		if chain[len(chain)-1] != before {
			return fmt.Errorf("discontinuous patch chain: %s", name)
		}
		chains[name] = append(chain, after)
		allowed[name][before] = true
		allowed[name][after] = true
	}
	for name, expected := range object(prepared, "files") {
		p, e := confined(source, name)
		if e != nil {
			return e
		}
		actual, e := digest(p)
		if e != nil {
			return e
		}
		if actual != expected && !allowed[name][actual] {
			return fmt.Errorf("undeclared upstream modification: %s", name)
		}
	}
	ordinals := map[string]int{}
	for _, entry := range series {
		patchFile, e := confined(a.path("native-core/patches"), stringValue(entry, "patch"))
		if e != nil {
			return e
		}
		patchBytes, e := os.ReadFile(patchFile)
		if e != nil {
			return e
		}
		if digestBytes(patchBytes) != entry["patch_sha256"] {
			return fmt.Errorf("patch checksum mismatch")
		}
		name := stringValue(entry, "path")
		target, e := confined(source, name)
		if e != nil {
			return e
		}
		original, e := os.ReadFile(target)
		if e != nil {
			return e
		}
		actual := digestBytes(original)
		ordinals[name]++
		applied := -1
		for i, hash := range chains[name] {
			if actual == hash {
				applied = i
				break
			}
		}
		if applied >= ordinals[name] {
			continue
		}
		if actual != entry["before_sha256"] {
			return fmt.Errorf("patch input checksum mismatch: %s", name)
		}
		changed, e := applyUnified(string(original), string(patchBytes))
		if e != nil {
			return e
		}
		if digestBytes([]byte(changed)) != entry["after_sha256"] {
			return fmt.Errorf("patch result checksum mismatch")
		}
		if e = writeChanged(target, []byte(changed)); e != nil {
			return e
		}
	}
	overlay := record{}
	for _, pattern := range []string{"native-core/overlay/*", "native-core/generated/*.h"} {
		paths, e := filepath.Glob(a.path(pattern))
		if e != nil {
			return e
		}
		for _, p := range paths {
			info, e := os.Stat(p)
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				continue
			}
			data, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			if e = writeChanged(filepath.Join(source, filepath.Base(p)), data); e != nil {
				return e
			}
			overlay[filepath.Base(p)] = digestBytes(data)
		}
	}
	vendor := a.path("native-core/vendor/yyjson")
	provenance, e := load(filepath.Join(vendor, "SOURCE.json"))
	if e != nil {
		return e
	}
	for name, expected := range object(provenance, "files") {
		p, e := confined(vendor, name)
		if e != nil {
			return e
		}
		data, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if digestBytes(data) != expected {
			return fmt.Errorf("yyjson source checksum mismatch")
		}
		if name != "LICENSE" {
			if e = writeChanged(filepath.Join(source, name), data); e != nil {
				return e
			}
			overlay[name] = expected
		}
	}
	version, e := productVersion(a)
	if e != nil {
		return e
	}
	nativeVersion := strings.TrimPrefix(stringValue(pin, "tag"), "v") + ".git-next." + version + "\n"
	if e = writeChanged(filepath.Join(source, "version"), []byte(nativeVersion)); e != nil {
		return e
	}
	overlay["version"] = digestBytes([]byte(nativeVersion))
	compatibility, e := nativeCompatibility(a)
	if e != nil {
		return e
	}
	identityJSON := `{"product":"git-next","version":` + pythonASCIIQuote(version) + `,"engine":"git-next native C core","compatibility_baseline":` + pythonASCIIQuote(stringValue(pin, "tag")) + `,"complete_git_parity":` + strconv.FormatBool(truth(compatibility, "complete_git_parity")) + `,"external_git_fallback":false,"external_ssh_fallback":false}`
	compatBytes, e := os.ReadFile(a.path("packaging/native-compatibility.json"))
	if e != nil {
		return e
	}
	compatJSON, e := orderedCompactJSON(compatBytes)
	if e != nil {
		return e
	}
	header := "/* Generated native product identity. SPDX-License-Identifier: GPL-2.0-only */\n"
	for _, pair := range [][2]string{{"GIT_NEXT_PRODUCT_VERSION", version}, {"GIT_NEXT_VERSION_JSON", identityJSON}, {"GIT_NEXT_COMPATIBILITY_JSON", compatJSON}} {
		header += "#define " + pair[0] + " " + pythonASCIIQuote(pair[1]) + "\n"
	}
	parity := "0"
	if truth(compatibility, "complete_git_parity") {
		parity = "1"
	}
	header += "#define GIT_NEXT_COMPLETE_GIT_PARITY " + parity + "\n#define GIT_NEXT_RESOURCE_VERSION " + strings.ReplaceAll(strings.SplitN(version, "-", 2)[0], ".", ",") + ",0\n#define GIT_NEXT_RESOURCE_VERSION_STRING " + strconv.Quote(version) + "\n"
	if e = writeChanged(filepath.Join(source, "git-next-identity.h"), []byte(header)); e != nil {
		return e
	}
	catalog, e := generateCatalog(a, source)
	if e != nil {
		return e
	}
	receipt := record{"status": "native-core-integrated-not-runtime-accepted", "upstream_commit": pin["commit"], "archive_sha256": pin["archive_sha256"], "patches": series, "overlay": overlay, "identity_sha256": digestBytes([]byte(header)), "catalog": catalog, "tests_executed": false, "runtime_accepted": false, "installed": false, "complete_git_parity": false}
	bytes, e := json.MarshalIndent(receipt, "", "  ")
	if e != nil {
		return e
	}
	if e = writeChanged(filepath.Join(source, "git-next-integration-receipt.json"), append(bytes, '\n')); e != nil {
		return e
	}
	return a.json(record{"status": receipt["status"], "patches": len(series), "overlay": len(overlay), "catalog": catalog, "runtime_accepted": false})
}
