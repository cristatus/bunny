package main

import (
	"reflect"
	"slices"
	"sort"
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
				// Kong flattens an embedded struct's flags into the command,
				// so a shared filter set has to be walked as if declared here.
				if sub.Anonymous && sub.Type.Kind() == reflect.Struct {
					cmd.flags = append(cmd.flags, embeddedFlags(sub.Type)...)
					continue
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

// embeddedFlags returns the flag names an embedded struct contributes, walking
// further embedding the way kong does.
func embeddedFlags(typ reflect.Type) []string {
	var out []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			out = append(out, embeddedFlags(f.Type)...)
			continue
		}
		if _, hasHelp := f.Tag.Lookup("help"); !hasHelp {
			continue
		}
		out = append(out, kongName(f.Name))
	}
	return out
}

// hiddenCommands must never be offered as completable subcommands. The scripts
// do invoke them, so the check is against the offered list, not the script text.
var hiddenCommands = map[string]bool{
	"complete-ids": true, "complete-tags": true, "complete-capabilities": true,
	"complete-catalogs": true, "complete-profiles": true, "complete-binaries": true,
	"netsetup": true,
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

// multiOperandCommandsFromCLI derives, from the CLI struct itself, which
// top-level commands accept more than one operand from the same completion
// source: a leading []string arg field without passthrough (passthrough
// means opaque trailing args like `run`'s, not more of the same thing to
// complete against — e.g. RunCmd.Args and NetsetupCmd.Argv are excluded).
func multiOperandCommandsFromCLI(t *testing.T) []string {
	t.Helper()
	var out []string
	typ := reflect.TypeOf(CLI{})
	for i := range typ.NumField() {
		f := typ.Field(i)
		if _, isCmd := f.Tag.Lookup("cmd"); !isCmd {
			continue
		}
		if _, hidden := f.Tag.Lookup("hidden"); hidden {
			continue
		}
		cmdType := f.Type
		for j := range cmdType.NumField() {
			sub := cmdType.Field(j)
			if _, isArg := sub.Tag.Lookup("arg"); !isArg {
				continue
			}
			if _, passthrough := sub.Tag.Lookup("passthrough"); passthrough {
				continue
			}
			if sub.Type.Kind() == reflect.Slice {
				out = append(out, kongName(f.Name))
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// completion.go's three script blobs hand-code which subcommands keep
// offering completions past the first operand (install/uninstall/search take
// several ids or terms against the same source; info/use/pin/etc. take
// exactly one). This test derives the same set from the CLI struct's actual
// types and fails if it drifts from completionMultiOperandSubcommands —
// which is the tripwire: `search`'s Query field is a []string exactly like
// install's IDs, but the scripts once treated it as single-operand and
// silently stopped completing after the first search term.
func TestCompletionMultiOperandCommandsMatchCLI(t *testing.T) {
	got := multiOperandCommandsFromCLI(t)
	want := slices.Clone(completionMultiOperandSubcommands)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("multi-operand commands drifted: CLI struct has %v, completionMultiOperandSubcommands has %v — update the constant AND the case patterns in all three script blobs", got, want)
	}
}
