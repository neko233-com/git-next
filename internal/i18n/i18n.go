// Package i18n implements the locale-document driven interface language.
//
// English is the source language and has no document of its own. Every other
// language is a JSON file embedded from locales/*.json; missing messages fall
// back to the English source text.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var catalogFiles embed.FS

const sourceLanguage = "en"

type catalog struct {
	Code        string            `json:"code"`
	Name        string            `json:"name"`
	EnglishName string            `json:"english_name"`
	Commands    map[string]string `json:"commands"`
	Messages    map[string]string `json:"messages"`
}

// Language is the resolved interface language.
type Language struct {
	Code        string `json:"language"`
	Name        string `json:"name"`
	EnglishName string `json:"english_name"`
}

var (
	loadOnce sync.Once
	catalogs []catalog
	loadErr  error
)

func all() ([]catalog, error) {
	loadOnce.Do(func() {
		entries, err := catalogFiles.ReadDir("locales")
		if err != nil {
			loadErr = err
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, err := catalogFiles.ReadFile("locales/" + entry.Name())
			if err != nil {
				loadErr = err
				return
			}
			var doc catalog
			if err := json.Unmarshal(data, &doc); err != nil {
				loadErr = fmt.Errorf("invalid locale %s: %w", entry.Name(), err)
				return
			}
			// The file name is the identity of a locale.
			doc.Code = strings.TrimSuffix(entry.Name(), ".json")
			catalogs = append(catalogs, doc)
		}
		catalogs = append(catalogs, catalog{
			Code: sourceLanguage, Name: "English", EnglishName: "English",
		})
		sort.Slice(catalogs, func(i, j int) bool { return catalogs[i].Code < catalogs[j].Code })
	})
	return catalogs, loadErr
}

var (
	activeMu       sync.RWMutex
	active         = Language{Code: sourceLanguage, Name: "English", EnglishName: "English"}
	activeSelected = "auto"
	activeSource   = "system"
)

func normalize(code string) string {
	return strings.ReplaceAll(strings.TrimSpace(code), "_", "-")
}

func find(code string) (Language, bool) {
	docs, err := all()
	if err != nil {
		return Language{}, false
	}
	wanted := strings.ToLower(normalize(code))
	for _, doc := range docs {
		if strings.ToLower(normalize(doc.Code)) == wanted {
			return Language{Code: doc.Code, Name: doc.Name, EnglishName: doc.EnglishName}, true
		}
	}
	return Language{}, false
}

func scriptForRegion(region string) string {
	switch region {
	case "cn", "sg", "my":
		return "hans"
	case "tw", "hk", "mo":
		return "hant"
	}
	return ""
}

// matchLocale walks from the most specific locale form to the least specific,
// resolving a writing system before dropping the region so zh-TW never lands
// on the Simplified Chinese document.
func matchLocale(value string) (Language, bool) {
	normalized := strings.ToLower(normalize(value))
	if normalized == "" || normalized == "c" || normalized == "posix" {
		return Language{}, false
	}
	parts := strings.Split(normalized, "-")
	candidates := []string{normalized}
	if len(parts) > 1 {
		if script := scriptForRegion(parts[len(parts)-1]); script != "" {
			withScript := parts[0] + "-" + script
			if !contains(candidates, withScript) {
				candidates = append(candidates, withScript)
			}
		}
		withoutRegion := strings.Join(parts[:len(parts)-1], "-")
		if !contains(candidates, withoutRegion) {
			candidates = append(candidates, withoutRegion)
		}
		if len(parts) > 2 && !contains(candidates, parts[0]) {
			candidates = append(candidates, parts[0])
		}
	}
	for _, candidate := range candidates {
		if language, ok := find(candidate); ok {
			return language, true
		}
	}
	return Language{}, false
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// Parse resolves a user supplied language name or device locale.
func Parse(value string) (string, Language, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", Language{}, fmt.Errorf("--language requires a value")
	}
	lower := strings.ToLower(trimmed)
	if lower == "auto" || lower == "system" {
		return "auto", Language{}, nil
	}
	aliases := map[string]string{
		"english": sourceLanguage, "en": sourceLanguage, "en-us": sourceLanguage, "en_us": sourceLanguage,
		"chinese": "zh-Hans", "zh": "zh-Hans", "zh-cn": "zh-Hans", "zh_cn": "zh-Hans",
		"zh-hans": "zh-Hans", "中文": "zh-Hans",
	}
	if code, ok := aliases[lower]; ok {
		language, _ := find(code)
		return code, language, nil
	}
	if language, ok := matchLocale(trimmed); ok {
		return language.Code, language, nil
	}
	return "", Language{}, fmt.Errorf("unsupported language; use one of the listed languages or auto")
}

func systemLanguage() Language {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		entries := strings.Split(value, ":")
		for _, entry := range entries {
			if index := strings.IndexAny(entry, ".@"); index >= 0 {
				entry = entry[:index]
			}
			if language, ok := matchLocale(entry); ok {
				return language
			}
		}
		return Language{Code: sourceLanguage, Name: "English", EnglishName: "English"}
	}
	return Language{Code: sourceLanguage, Name: "English", EnglishName: "English"}
}

func settingsPath() (string, error) {
	if root := os.Getenv("GIT_NEXT_CONFIG_HOME"); root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("GIT_NEXT_CONFIG_HOME must be an absolute directory")
		}
		return filepath.Join(root, "settings.json"), nil
	}
	var root string
	if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
		root = value
	} else if value := os.Getenv("HOME"); value != "" {
		root = filepath.Join(value, ".config")
	} else if value := os.Getenv("USERPROFILE"); value != "" {
		root = filepath.Join(value, ".config")
	} else {
		return "", fmt.Errorf("HOME/USERPROFILE is unset")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("language settings require an absolute user configuration directory")
	}
	return filepath.Join(root, "git-next", "settings.json"), nil
}

func readSettings(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "auto", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read git-next language settings: %w", err)
	}
	if len(data) > 65536 {
		return "", fmt.Errorf("git-next language settings exceed the size limit")
	}
	var value struct {
		SchemaVersion int    `json:"schema_version"`
		Language      string `json:"language"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("invalid git-next language settings; existing file was preserved: %w", err)
	}
	if value.SchemaVersion != 1 || value.Language == "" {
		return "", fmt.Errorf("unsupported git-next settings schema")
	}
	return value.Language, nil
}

// Initialize selects the active language from the command line, environment,
// persisted settings or the device. The first source that is present wins.
func Initialize(args []string) error {
	var explicit string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == "--language" {
			if i+1 >= len(args) {
				return fmt.Errorf("--language requires a value")
			}
			i++
			explicit = args[i]
			continue
		}
		if value, ok := strings.CutPrefix(arg, "--language="); ok {
			explicit = value
		}
	}
	var selected, source string
	var language Language
	if explicit != "" {
		selection, resolved, err := Parse(explicit)
		if err != nil {
			return err
		}
		selected, language, source = selection, resolved, "command-line"
	} else if value := os.Getenv("GIT_NEXT_LANGUAGE"); value != "" {
		selection, resolved, err := Parse(value)
		if err != nil {
			return err
		}
		selected, language, source = selection, resolved, "environment"
	} else if path, err := settingsPath(); err == nil {
		value, err := readSettings(path)
		if err != nil {
			return err
		}
		selection, resolved, err := Parse(value)
		if err != nil {
			return err
		}
		selected = selection
		source = "settings"
		if selection == "auto" {
			source = "system"
		}
		language = resolved
	} else if os.Getenv("GIT_NEXT_CONFIG_HOME") != "" {
		return err
	}
	if selected == "" {
		selected = "auto"
	}
	if language.Code == "" {
		language = systemLanguage()
	}
	activeMu.Lock()
	active, activeSelected, activeSource = language, selected, source
	activeMu.Unlock()
	return nil
}

// Current returns the active language and its selection metadata.
func Current() (Language, string, string) {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return active, activeSelected, activeSource
}

func ActiveLanguage() string {
	language, _, _ := Current()
	return language.Code
}

func isSource() bool { return ActiveLanguage() == sourceLanguage }

// Text translates an English source message, falling back to the source.
func Text(english string) string {
	if isSource() {
		return english
	}
	docs, err := all()
	if err != nil {
		return english
	}
	code := ActiveLanguage()
	for _, doc := range docs {
		if doc.Code == code {
			if translated, ok := doc.Messages[english]; ok {
				return translated
			}
			return english
		}
	}
	return english
}

// Format replaces named placeholders in a translated template.
func Format(template string, values map[string]string) string {
	template = Text(template)
	var out strings.Builder
	for i := 0; i < len(template); {
		if template[i] != '{' {
			out.WriteByte(template[i])
			i++
			continue
		}
		end := strings.IndexByte(template[i+1:], '}')
		if end < 0 {
			out.WriteString(template[i:])
			break
		}
		key := template[i+1 : i+1+end]
		if value, ok := values[key]; ok {
			out.WriteString(value)
		} else {
			out.WriteString(template[i : i+end+2])
		}
		i += end + 2
	}
	return out.String()
}

// CommandAbout returns a translated command summary, if one exists.
func CommandAbout(name string) string {
	if isSource() {
		return ""
	}
	docs, err := all()
	if err != nil {
		return ""
	}
	code := ActiveLanguage()
	for _, doc := range docs {
		if doc.Code == code {
			return doc.Commands[name]
		}
	}
	return ""
}

// Supported returns all shipped language codes, sorted.
func Supported() []string {
	docs, err := all()
	if err != nil {
		return []string{sourceLanguage}
	}
	codes := make([]string, 0, len(docs))
	for _, doc := range docs {
		codes = append(codes, doc.Code)
	}
	return codes
}

func atomicWrite(path string, data []byte) error {
	lock := path + ".lock"
	file, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("another writer holds the lock: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(lock)
		}
	}()
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(lock, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// Run implements the `language` command.
func Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("language requires get, set or list")
	}
	switch args[0] {
	case "list":
		docs, err := all()
		if err != nil {
			return err
		}
		for _, doc := range docs {
			english := doc.EnglishName
			if english == "" {
				english = doc.Name
			}
			fmt.Printf("%s\t%s\t%s\n", doc.Code, doc.Name, english)
		}
		fmt.Printf("auto\t%s\n", Text("Follow the device language"))
		return nil
	case "get":
		language, selection, source := Current()
		if len(args) > 1 && args[1] == "--json" {
			report := struct {
				SchemaVersion int      `json:"schema_version"`
				Language      string   `json:"language"`
				Name          string   `json:"name"`
				EnglishName   string   `json:"english_name"`
				Configured    string   `json:"configured"`
				Source        string   `json:"source"`
				Supported     []string `json:"supported"`
			}{1, language.Code, language.Name, language.EnglishName, selection, source, Supported()}
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		}
		fmt.Println(language.Code)
		return nil
	case "set":
		if len(args) < 2 {
			return fmt.Errorf("language set requires a language")
		}
		selection, resolved, err := Parse(args[1])
		if err != nil {
			return err
		}
		path, err := settingsPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		current, err := readSettings(path)
		if err != nil {
			return err
		}
		value := map[string]any{"schema_version": 1, "language": current}
		value["language"] = selection
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		if err := atomicWrite(path, append(data, '\n')); err != nil {
			return err
		}
		if resolved.Code == "" {
			resolved = systemLanguage()
		}
		fmt.Println(Format("Language set to {name} ({code}).", map[string]string{
			"name": resolved.Name, "code": resolved.Code,
		}))
		return nil
	default:
		return fmt.Errorf("unknown language action %q; use get, set or list", args[0])
	}
}
