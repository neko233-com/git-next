package cli

import "testing"

func TestParseGlobalOptions(t *testing.T) {
	tests := []struct {
		name      string
		argv      []string
		command   string
		language  string
		args      []string
		wantError bool
	}{
		{
			name:    "language before command",
			argv:    []string{"git", "--language", "english", "status", "--porcelain"},
			command: "status", language: "english", args: []string{"--porcelain"},
		},
		{
			name:    "attached language and cwd",
			argv:    []string{"git", "--language=english", "-C", ".", "rev-parse", "--git-dir"},
			command: "rev-parse", language: "english", args: []string{"--git-dir"},
		},
		{
			name:      "unknown command",
			argv:      []string{"git", "not-a-command"},
			wantError: true,
		},
		{
			name:      "unknown option",
			argv:      []string{"git", "--does-not-exist", "status"},
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inv, err := Parse(test.argv)
			if test.wantError {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if inv.Command != test.command || inv.Language != test.language {
				t.Fatalf("got command=%q language=%q", inv.Command, inv.Language)
			}
			if len(inv.Args) != len(test.args) {
				t.Fatalf("got args %v", inv.Args)
			}
			for i := range test.args {
				if inv.Args[i] != test.args[i] {
					t.Fatalf("args[%d]=%q, want %q", i, inv.Args[i], test.args[i])
				}
			}
		})
	}
}

func TestParseCommandFlags(t *testing.T) {
	tests := []struct {
		name  string
		spec  Spec
		args  []string
		check func(*Parsed) error
	}{
		{
			name: "bare porcelain uses default", spec: Specs["status"], args: []string{"--porcelain"},
			check: func(p *Parsed) error {
				if p.Get("porcelain") != "v1" {
					t.Errorf("porcelain=%q", p.Get("porcelain"))
				}
				return nil
			},
		},
		{
			name: "grouped short flags", spec: Specs["status"], args: []string{"-sb"},
			check: func(p *Parsed) error {
				if !p.Has("short") || !p.Has("branch") {
					t.Errorf("short=%v branch=%v", p.Has("short"), p.Has("branch"))
				}
				return nil
			},
		},
		{
			name: "attached untracked value", spec: Specs["status"], args: []string{"-uno"},
			check: func(p *Parsed) error {
				if p.Get("untracked-files") != "no" {
					t.Errorf("untracked=%q", p.Get("untracked-files"))
				}
				return nil
			},
		},
		{
			name: "message value", spec: Specs["commit"], args: []string{"-m", "hello"},
			check: func(p *Parsed) error {
				if p.Get("message") != "hello" {
					t.Errorf("message=%q", p.Get("message"))
				}
				return nil
			},
		},
		{
			name: "initial branch", spec: Specs["init"], args: []string{"-b", "main", "."},
			check: func(p *Parsed) error {
				if p.Get("initial-branch") != "main" || len(p.Positional()) != 1 {
					t.Errorf("branch=%q args=%v", p.Get("initial-branch"), p.Positional())
				}
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := ParseCommand(test.spec, test.args)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.check(parsed); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestParseCommandRejectsUnknownFlag(t *testing.T) {
	if _, err := ParseCommand(Specs["status"], []string{"--not-a-flag"}); err == nil {
		t.Fatal("expected unknown flag error")
	}
}
