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

// Electron update feeds (latest-*.yml) state the digest in base64, and it is
// mixed case, so the hex path's lowercasing has to not run first.
func TestParseChecksumPatternDecodesBase64(t *testing.T) {
	feed := `version: 2.8.1
files:
  - url: https://example.com/App.deb
    sha512: Q2ThRCZ/Bs6UVAujM+zyyNRyfjjBfzmM/j2ne5mHPFwXkt9SlzvTJsYdoT+7Hj+iyfmXTzmMhpMLS1BLv1QE+A==
    size: 122979528`
	const want = "4364e144267f06ce94540ba333ecf2c8d4727e38c17f398cfe3da77b99873c5c" +
		"1792df52973bd326c61da13fbb1e3fa2c9f9974f398c86930b4b504bbf5404f8"

	got, algorithm, err := ParseChecksumPattern(feed, `App\.deb\s+sha512: (\S+)`)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || algorithm != "sha512" {
		t.Fatalf("got (%q, %q), want (%q, sha512)", got, algorithm, want)
	}
}

func TestDecodeBase64DigestRejectsWrongLength(t *testing.T) {
	// Valid base64, but decodes to neither 32 nor 64 bytes.
	if _, _, ok := decodeBase64Digest("aGVsbG8="); ok {
		t.Fatal("expected a non-digest-length payload to be rejected")
	}
}
