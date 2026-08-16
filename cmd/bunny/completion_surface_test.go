package main

import (
	"reflect"
	"strings"
	"testing"
	"unicode"
)

// kongName converts a Go field name to the flag/command name kong derives from
// it: NoProgress -> no-progress.
func kongName(field string) string {
	var b strings.Builder
	for i, r := range field {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('-')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// command is one completable command and the flags it accepts.
type command struct {
	name  string // "install", or "dev update" for a nested one
	flags []string
}

// cliSurface walks the CLI struct the way kong does. Deriving the truth from
// the struct is the point: a hand-maintained list is exactly what drifts, and
// drift here is invisible until someone tabs for a flag that never appears.
func cliSurface(t *testing.T) []command {
	t.Helper()
	var out []command

	var walk func(typ reflect.Type, prefix string)
	walk = func(typ reflect.Type, prefix string) {
		for i := range typ.NumField() {
			f := typ.Field(i)
			if _, isCmd := f.Tag.Lookup("cmd"); !isCmd {
				continue
			}
			name := strings.TrimSpace(prefix + " " + kongName(f.Name))
			cmd := command{name: name}
			nested := false
			for j := range f.Type.NumField() {
				sub := f.Type.Field(j)
				if _, isSub := sub.Tag.Lookup("cmd"); isSub {
					nested = true
					continue
				}
				if _, isArg := sub.Tag.Lookup("arg"); isArg {
					continue // positional, not a flag
				}
				if _, hasHelp := sub.Tag.Lookup("help"); !hasHelp {
					continue
				}
				cmd.flags = append(cmd.flags, kongName(sub.Name))
			}
			if nested {
				walk(f.Type, name)
				continue // a group, not a leaf command
			}
			out = append(out, cmd)
		}
	}
	walk(reflect.TypeOf(CLI{}), "")
	return out
}

// hiddenCommands must never be offered as completable subcommands. The scripts
// do invoke them, so the check is against the offered list, not the script text.
var hiddenCommands = map[string]bool{
	"complete-ids": true, "complete-tags": true, "complete-capabilities": true,
}

// flagText is how a shell's script spells a long flag: bash and zsh complete
// "--force", fish declares it as "-l force".
func flagText(shell, flag string) string {
	if shell == "fish" {
		return "-l " + flag
	}
	return "--" + flag
}

// Every command kong accepts must be offered by every script, and every flag it
// takes must be completable. Without this the scripts drift silently: `dev
// validate` and `uninstall --yes` were both missing when it was written.
func TestCompletionCoversTheCLI(t *testing.T) {
	surface := cliSurface(t)
	if len(surface) < 15 {
		t.Fatalf("walked only %d commands, the CLI has many more", len(surface))
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script := completionScript(shell)
		for _, cmd := range surface {
			if hiddenCommands[cmd.name] {
				for _, offered := range completionSubcommands {
					if offered == cmd.name {
						t.Errorf("hidden helper %q must not be offered as a subcommand", cmd.name)
					}
				}
				continue
			}
			for _, word := range strings.Fields(cmd.name) {
				if !strings.Contains(script, word) {
					t.Errorf("%s: command %q is not completable", shell, cmd.name)
				}
			}
			for _, flag := range cmd.flags {
				if !strings.Contains(script, flagText(shell, flag)) {
					t.Errorf("%s: %s --%s is not completable", shell, cmd.name, flag)
				}
			}
		}
	}
}

// Global flags are accepted before any subcommand, so each script has to offer
// them on their own.
func TestCompletionCoversGlobalFlags(t *testing.T) {
	typ := reflect.TypeOf(CLI{})
	var want []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		if _, isCmd := f.Tag.Lookup("cmd"); isCmd {
			continue
		}
		if _, hasHelp := f.Tag.Lookup("help"); !hasHelp {
			continue
		}
		want = append(want, kongName(f.Name))
	}
	for _, flag := range want {
		if !strings.Contains(strings.Join(completionGlobalFlags, " "), flag) {
			t.Errorf("global flag --%s missing from completionGlobalFlags", flag)
		}
		for _, shell := range []string{"bash", "zsh", "fish"} {
			if !strings.Contains(completionScript(shell), flag) {
				t.Errorf("%s script missing global flag --%s", shell, flag)
			}
		}
	}
}
