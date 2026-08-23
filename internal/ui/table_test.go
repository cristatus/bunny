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

func TestClip(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		w        int
	}{
		{in: "short", w: 10, want: "short"},
		{in: "exactly-ten", w: 11, want: "exactly-ten"},
		{in: "a long description", w: 8, want: "a long…"},
		{in: "trailing space cut", w: 10, want: "trailing…"},
		{in: "multibyte — dash", w: 12, want: "multibyte —…"},
		{in: "anything", w: 0, want: "anything"},
	} {
		if got := clip(tc.in, tc.w); got != tc.want {
			t.Errorf("clip(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
		if got := clip(tc.in, tc.w); dispWidth(got) > tc.w && tc.w > 0 {
			t.Errorf("clip(%q, %d) = %q, wider than asked", tc.in, tc.w, got)
		}
	}
}

// Clipping is a terminal courtesy. A buffer has no width, so nothing is cut:
// piping the output has to give a script the whole text.
func TestTableNeverClipsWhenNotATerminal(t *testing.T) {
	var b bytes.Buffer
	p := NewWithColor(&b, false)
	long := strings.Repeat("very long description ", 20)
	out := p.Table([]string{"Package", "Description"}, [][]Cell{{{Text: "jdk-21"}, {Text: long}}})
	if !strings.Contains(out, long) {
		t.Error("piped output must carry the whole cell text")
	}
	if strings.Contains(out, "…") {
		t.Error("piped output must not be clipped")
	}
}

func TestFitWidth(t *testing.T) {
	// "Package  Description": 7 + 2 gutter leaves 71 of an 80-column terminal.
	for _, tc := range []struct {
		name   string
		term   int
		widths []int
		want   int
	}{
		{name: "not a terminal", term: 0, widths: []int{7, 200}, want: 0},
		{name: "already fits", term: 80, widths: []int{7, 40}, want: 0},
		{name: "clipped to what is left", term: 80, widths: []int{7, 200}, want: 71},
		{name: "too little room to read", term: 40, widths: []int{7, 20, 200}, want: 0},
		{name: "single column is never clipped", term: 20, widths: []int{200}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fitWidth(tc.term, tc.widths); got != tc.want {
				t.Errorf("fitWidth(%d, %v) = %d, want %d", tc.term, tc.widths, got, tc.want)
			}
		})
	}
}
