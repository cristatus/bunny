package ui

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTablePlainAlignsAndTrims(t *testing.T) {
	var b bytes.Buffer
	p := NewWithColor(&b, false)
	got := p.Table(
		[]string{"Package", "Version"},
		[][]Cell{
			{{Text: "jdk-21"}, {Text: "21.0.11"}},
			{{Text: "code"}, {Text: "1.128.0"}},
		},
	)
	want := "Package  Version\n" +
		"jdk-21   21.0.11\n" +
		"code     1.128.0\n"
	if got != want {
		t.Fatalf("Table plain =\n%q\nwant\n%q", got, want)
	}
}

func TestTableDropsTrailingEmptyCellsNoTrailingSpace(t *testing.T) {
	var b bytes.Buffer
	p := NewWithColor(&b, false)
	got := p.Table(
		[]string{"Package", "Provides"},
		[][]Cell{{{Text: "code"}, {Text: ""}}},
	)
	want := "Package  Provides\n" + "code\n"
	if got != want {
		t.Fatalf("Table =\n%q\nwant\n%q", got, want)
	}
}

func TestKVPlain(t *testing.T) {
	var b bytes.Buffer
	p := NewWithColor(&b, false)
	// Keys are right-aligned (colons line up) with a "key:" separator.
	got := p.KV([]KVRow{{"Id", "x"}, {"Version", "1.0"}})
	want := "     Id: x\n" + "Version: 1.0\n"
	if got != want {
		t.Fatalf("KV =\n%q\nwant\n%q", got, want)
	}
}

func TestTableAlignsMultibyteGlyph(t *testing.T) {
	var b bytes.Buffer
	p := NewWithColor(&b, false)
	got := p.Table([]string{"Change", "Bump"}, [][]Cell{
		{{Text: "1.0 → 1.1"}, {Text: "minor"}},
		{{Text: "1.11.0 → 1.12.0"}, {Text: "patch"}},
	})
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	col := func(s, needle string) int { // display column, rune-based
		return utf8.RuneCountInString(s[:strings.Index(s, needle)])
	}
	h, r1, r2 := col(lines[0], "Bump"), col(lines[1], "minor"), col(lines[2], "patch")
	if h != r1 || h != r2 {
		t.Fatalf("second column misaligned with a → cell: header@%d r1@%d r2@%d\n%s", h, r1, r2, got)
	}
}
