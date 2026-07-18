package checker

import "testing"

func TestParseChecksumPattern(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	formula := `require 'formula'

class Example < Formula
  url 'https://example.com/example.tar.gz'
  sha256 '` + hash + `'
end`

	got, algorithm, err := ParseChecksumPattern(formula, `sha256 '([a-f0-9]{64})'`)
	if err != nil {
		t.Fatal(err)
	}
	if got != hash || algorithm != "sha256" {
		t.Fatalf("got (%q, %q), want (%q, sha256)", got, algorithm, hash)
	}
}

func TestParseChecksumPatternRejectsUnmatchedDigest(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, _, err := ParseChecksumPattern("dependency "+hash, `sha256 '([a-f0-9]{64})'`); err == nil {
		t.Fatal("expected a digest outside the declared pattern to be rejected")
	}
}
