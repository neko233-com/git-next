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
	"unicode/utf8"
)

func init() {
	register("check-locales", "Validate locale structure, keys, placeholders and whitespace", "coverage? fix?", checkLocales)
	register("extract-locale", "Extract the legacy Rust Chinese catalog as locale JSON", "source output", extractLocale)
}

type jsonOrder struct {
	keys       map[string][]string
	duplicates []string
}

func orderedJSON(data []byte) (record, jsonOrder, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	order := jsonOrder{keys: map[string][]string{}}
	var value func(string) (any, error)
	value = func(path string) (any, error) {
		t, e := d.Token()
		if e != nil {
			return nil, e
		}
		delimiter, ok := t.(json.Delim)
		if !ok {
			return t, nil
		}
		if delimiter == '{' {
			m := record{}
			for d.More() {
				token, e := d.Token()
				if e != nil {
					return nil, e
				}
				key, ok := token.(string)
				if !ok {
					return nil, fmt.Errorf("JSON object key must be text")
				}
				if _, exists := m[key]; exists {
					order.duplicates = append(order.duplicates, path+"/"+key)
				}
				order.keys[path] = append(order.keys[path], key)
				v, e := value(path + "/" + key)
				if e != nil {
					return nil, e
				}
				m[key] = v
			}
			_, e = d.Token()
			return m, e
		}
		if delimiter == '[' {
			var a []any
			for d.More() {
				v, e := value(path + "/[]")
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			_, e = d.Token()
			return a, e
		}
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
	v, e := value("")
	if e != nil {
		return nil, order, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, order, fmt.Errorf("trailing JSON content")
	}
	m, ok := v.(record)
	if !ok {
		return nil, order, fmt.Errorf("JSON document must be an object")
	}
	return m, order, nil
}

var placeholderPattern = regexp.MustCompile(`\{([a-z_][a-z0-9_]*)\}`)

func placeholders(s string) []string {
	values := map[string]bool{}
	for _, m := range placeholderPattern.FindAllStringSubmatch(s, -1) {
		if len(m[1]) > 1 {
			values[m[1]] = true
		}
	}
	out := make([]string, 0, len(values))
	for name := range values {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
func rustLiteral(s string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strconv.Quote(s), `"`), `"`)
}
func localeCorpus(a *app) (string, error) {
	var all strings.Builder
	paths, e := files(a.path("src"))
	if e != nil {
		return "", e
	}
	for _, p := range paths {
		if strings.HasSuffix(p, ".rs") {
			data, e := read(p)
			if e != nil {
				return "", e
			}
			all.WriteString(data)
			all.WriteByte('\n')
		}
	}
	paths, e = filepath.Glob(a.path("packaging/*.json"))
	if e != nil {
		return "", e
	}
	paths = append(paths, a.path("compatibility.json"))
	for _, p := range paths {
		data, e := read(p)
		if e != nil {
			return "", e
		}
		all.WriteString(data)
		all.WriteByte('\n')
	}
	return all.String(), nil
}
func commandNames(a *app) (map[string]bool, error) {
	text, e := read(a.path("src/cli.rs"))
	if e != nil {
		return nil, e
	}
	_, body, ok := strings.Cut(text, "enum Commands")
	if !ok {
		return nil, fmt.Errorf("Commands enum missing")
	}
	body, _, _ = strings.Cut(body, "\n}")
	names := map[string]bool{"git": true}
	pattern := regexp.MustCompile(`(?m)^    ([A-Z][A-Za-z0-9]*)\s*(\{|,|\()`)
	for _, m := range pattern.FindAllStringSubmatch(body, -1) {
		var name strings.Builder
		for i, c := range m[1] {
			if i > 0 && c >= 'A' && c <= 'Z' {
				name.WriteByte('-')
			}
			name.WriteRune(c)
		}
		names[strings.ToLower(name.String())] = true
	}
	return names, nil
}
func validateLocale(name string, data []byte, corpus string, commands map[string]bool) (record, []string) {
	var problems []string
	issue := func(format string, args ...any) { problems = append(problems, name+": "+fmt.Sprintf(format, args...)) }
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		issue("starts with a byte order mark")
	}
	if bytes.Contains(data, []byte("\r\n")) {
		issue("uses CRLF line endings")
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		issue("does not end with a newline")
	}
	if !utf8.Valid(data) {
		issue("is not valid UTF-8")
		return nil, problems
	}
	document, order, e := orderedJSON(data)
	if e != nil {
		issue("invalid JSON: %s", e)
		return nil, problems
	}
	for _, key := range order.duplicates {
		issue("duplicate key %q", key)
	}
	if stringValue(document, "code") != strings.TrimSuffix(name, ".json") {
		issue("code does not match filename")
	}
	for _, field := range []string{"name", "english_name"} {
		if strings.TrimSpace(stringValue(document, field)) == "" {
			issue("%s is missing or empty", field)
		}
	}
	for _, field := range []string{"messages", "commands"} {
		if document[field] == nil {
			continue
		}
		section, ok := document[field].(map[string]any)
		if !ok {
			issue("%s is not an object", field)
			continue
		}
		keys := order.keys["/"+field]
		if !sort.StringsAreSorted(keys) {
			issue("%s is not sorted; run --fix", field)
		}
		for key, raw := range section {
			translation, ok := raw.(string)
			if !ok || strings.TrimSpace(translation) == "" {
				issue("%s %q is not a nonempty string", field, key)
				continue
			}
			if field == "commands" {
				if !commands[key] {
					issue("unknown command %q", key)
				}
				continue
			}
			if !strings.Contains(corpus, `"`+rustLiteral(key)+`"`) && !strings.Contains(corpus, key) {
				issue("message %q does not appear in the source", key)
			}
			if strings.Join(placeholders(key), "\x00") != strings.Join(placeholders(translation), "\x00") {
				issue("message %q has mismatched placeholders", key)
			}
			for _, suffix := range []string{"\n", "\n\n"} {
				if strings.HasSuffix(key, suffix) != strings.HasSuffix(translation, suffix) {
					issue("message %q has mismatched trailing newlines", key)
					break
				}
			}
		}
	}
	return document, problems
}
func checkLocales(a *app, o options) error {
	paths, e := filepath.Glob(a.path("locales/*.json"))
	if e != nil {
		return e
	}
	if len(paths) == 0 {
		return fmt.Errorf("no locale documents")
	}
	corpus, e := localeCorpus(a)
	if e != nil {
		return e
	}
	names, e := commandNames(a)
	if e != nil {
		return e
	}
	var problems []string
	docs := map[string]record{}
	for _, p := range paths {
		if o.yes("fix") {
			m, e := load(p)
			if e != nil {
				return e
			}
			if e = writeJSON(p, m); e != nil {
				return e
			}
		}
		data, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		m, errors := validateLocale(filepath.Base(p), data, corpus, names)
		problems = append(problems, errors...)
		if m != nil {
			docs[filepath.Base(p)] = m
		}
	}
	if o.yes("coverage") {
		known := map[string]bool{}
		for _, doc := range docs {
			for key := range object(doc, "messages") {
				known[key] = true
			}
		}
		fmt.Fprintf(a.out, "%d messages known; %d commands declared\n", len(known), len(names))
		sort.Strings(paths)
		for _, p := range paths {
			doc := docs[filepath.Base(p)]
			fmt.Fprintf(a.out, "%-16s messages %d/%d commands %d\n", filepath.Base(p), len(object(doc, "messages")), len(known), len(object(doc, "commands")))
		}
	}
	sort.Strings(problems)
	for _, problem := range problems {
		fmt.Fprintln(a.err, "error:", problem)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s) in %d locale documents", len(problems), len(paths))
	}
	fmt.Fprintf(a.out, "%d locale documents checked: structure, stale keys, placeholders, whitespace, order\n", len(paths))
	return nil
}
func extractLocale(a *app, o options) error {
	source, e := read(o.value("source", a.path("src/language/catalog.rs")))
	if e != nil {
		return e
	}
	body := func(name string) (string, error) {
		start := strings.Index(source, "fn "+name)
		if start < 0 {
			return "", fmt.Errorf("function %s not found in catalog", name)
		}
		offset := strings.Index(source[start:], "{")
		if offset < 0 {
			return "", fmt.Errorf("function %s lacks body", name)
		}
		start += offset
		depth := 0
		for i := start; i < len(source); i++ {
			if source[i] == '"' {
				_, after, err := catalogLiteral(source, i)
				if err != nil {
					return "", err
				}
				i = after - 1
				continue
			}
			if source[i] == '{' {
				depth++
			}
			if source[i] == '}' {
				depth--
				if depth == 0 {
					return source[start+1 : i], nil
				}
			}
		}
		return "", fmt.Errorf("unbalanced function %s", name)
	}
	parse := func(name string) (map[string]string, error) {
		text, e := body(name)
		if e != nil {
			return nil, e
		}
		return catalogArms(text)
	}
	messages, e := parse("chinese")
	if e != nil {
		return e
	}
	abouts, e := parse("command_about")
	if e != nil {
		return e
	}
	for command, english := range abouts {
		if translated := messages[english]; translated != "" {
			abouts[command] = translated
		}
	}
	path := o.value("output", a.path("locales/zh-Hans.json"))
	if e = writeJSON(path, record{"code": "zh-Hans", "name": "简体中文", "english_name": "Simplified Chinese", "messages": messages, "commands": abouts}); e != nil {
		return e
	}
	return a.json(record{"output": path, "messages": len(messages), "commands": len(abouts)})
}

func catalogLiteral(text string, start int) (string, int, error) {
	if start >= len(text) || text[start] != '"' {
		return "", start, fmt.Errorf("catalog literal expected")
	}
	var b strings.Builder
	for i := start + 1; i < len(text); i++ {
		if text[i] == '"' {
			return b.String(), i + 1, nil
		}
		if text[i] != '\\' {
			b.WriteByte(text[i])
			continue
		}
		i++
		if i >= len(text) {
			return "", i, fmt.Errorf("unterminated catalog escape")
		}
		switch text[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '0':
			b.WriteByte(0)
		case '\\', '"':
			b.WriteByte(text[i])
		case 'x':
			if i+2 >= len(text) {
				return "", i, fmt.Errorf("short catalog hex escape")
			}
			n, e := strconv.ParseUint(text[i+1:i+3], 16, 8)
			if e != nil {
				return "", i, e
			}
			b.WriteByte(byte(n))
			i += 2
		case 'u':
			if i+1 >= len(text) || text[i+1] != '{' {
				return "", i, fmt.Errorf("Rust unicode escape requires braces")
			}
			end := strings.IndexByte(text[i+2:], '}')
			if end < 0 {
				return "", i, fmt.Errorf("unterminated Unicode escape")
			}
			n, e := strconv.ParseUint(strings.ReplaceAll(text[i+2:i+2+end], "_", ""), 16, 32)
			if e != nil || !utf8.ValidRune(rune(n)) {
				return "", i, fmt.Errorf("invalid catalog Unicode escape")
			}
			b.WriteRune(rune(n))
			i += end + 2
		case '\n':
			for i+1 < len(text) && strings.ContainsRune(" \t\r\n", rune(text[i+1])) {
				i++
			}
		default:
			return "", i, fmt.Errorf("unsupported Rust string escape")
		}
	}
	return "", len(text), fmt.Errorf("unterminated catalog literal")
}
func catalogArms(text string) (map[string]string, error) {
	values := map[string]string{}
	skipSpace := func(i int) int {
		for i < len(text) && strings.ContainsRune(" \t\r\n", rune(text[i])) {
			i++
		}
		return i
	}
	for i := 0; i < len(text); {
		if text[i] != '"' {
			i++
			continue
		}
		key, after, e := catalogLiteral(text, i)
		if e != nil {
			return nil, e
		}
		cursor := skipSpace(after)
		if !strings.HasPrefix(text[cursor:], "=>") {
			i = after
			continue
		}
		cursor = skipSpace(cursor + 2)
		if cursor < len(text) && text[cursor] == '{' {
			cursor = skipSpace(cursor + 1)
		}
		if cursor >= len(text) || text[cursor] != '"' {
			i = after
			continue
		}
		value, end, e := catalogLiteral(text, cursor)
		if e != nil {
			return nil, e
		}
		values[key] = value
		i = end
	}
	return values, nil
}
