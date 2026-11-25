package i18n

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		value    string
		selected string
		code     string
		wantErr  bool
	}{
		{"english", "en", "en", false},
		{"zh-TW", "zh-Hant", "zh-Hant", false},
		{"zh-CN", "zh-Hans", "zh-Hans", false},
		{"ja-JP", "ja", "ja", false},
		{"auto", "auto", "", false},
		{"not-a-language", "", "", true},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			selected, language, err := Parse(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if selected != test.selected || language.Code != test.code {
				t.Fatalf("selected=%q code=%q", selected, language.Code)
			}
		})
	}
}

func TestTextFallsBackAndTranslates(t *testing.T) {
	activeMu.Lock()
	previous, previousSelected, previousSource := active, activeSelected, activeSource
	active = Language{Code: "zh-Hant", Name: "繁體中文", EnglishName: "Traditional Chinese"}
	activeMu.Unlock()
	defer func() {
		activeMu.Lock()
		active, activeSelected, activeSource = previous, previousSelected, previousSource
		activeMu.Unlock()
	}()
	key := "  (use \"git add <file>...\" to update what will be committed)\n"
	if got := Text(key); got == key {
		t.Fatal("expected a Traditional Chinese translation")
	}
	if got := Text("this source string is not translated"); got != "this source string is not translated" {
		t.Fatalf("fallback failed: %q", got)
	}
}

func TestInitializeCommandLineWins(t *testing.T) {
	t.Setenv("GIT_NEXT_LANGUAGE", "de")
	t.Setenv("GIT_NEXT_CONFIG_HOME", t.TempDir())
	activeMu.Lock()
	previous, previousSelected, previousSource := active, activeSelected, activeSource
	activeMu.Unlock()
	defer func() {
		activeMu.Lock()
		active, activeSelected, activeSource = previous, previousSelected, previousSource
		activeMu.Unlock()
	}()
	if err := Initialize([]string{"git", "--language", "zh-TW", "status"}); err != nil {
		t.Fatal(err)
	}
	language, selected, source := Current()
	if language.Code != "zh-Hant" || selected != "zh-Hant" || source != "command-line" {
		t.Fatalf("language=%+v selected=%q source=%q", language, selected, source)
	}
}

func TestSupportedIncludesSource(t *testing.T) {
	found := false
	for _, code := range Supported() {
		if code == "en" {
			found = true
		}
	}
	if !found {
		t.Fatal("English source language is missing")
	}
}
