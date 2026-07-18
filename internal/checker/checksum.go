package checker

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/log"
)

// FetchChecksum tries common URL patterns (.sha256, .sha512, .checksum) sibling
// to downloadURL and returns the first hash that parses cleanly.
func FetchChecksum(ctx context.Context, downloadURL string) (hash, algorithm string, err error) {
	patterns := []struct {
		suffix    string
		algorithm string
		validator func(string) bool
	}{
		{".sha256", "sha256", IsValidSHA256},
		{".sha512", "sha512", IsValidSHA512},
		{".checksum", "", nil},
	}
	for _, p := range patterns {
		u := downloadURL + p.suffix
		log.Debug("Trying checksum URL", "url", u)
		if h, a, err := fetchAndParse(ctx, u, filepath.Base(downloadURL), p.algorithm, p.validator); err == nil {
			return h, a, nil
		}
	}
	return "", "", fmt.Errorf("no checksum found")
}

// FetchChecksumFromURL fetches an explicit checksum URL and extracts the hash
// for targetFile. When hashPattern is set, its first capture group identifies
// the digest; otherwise common checksum-file formats are parsed.
func FetchChecksumFromURL(ctx context.Context, hashURL, targetFile, hashPattern string) (hash, algorithm string, err error) {
	log.Debug("Fetching checksum", "url", hashURL)
	body, err := httpReadAll(ctx, hashURL)
	if err != nil {
		return "", "", err
	}
	var validator func(string) bool
	urlLower := strings.ToLower(hashURL)
	switch {
	case strings.Contains(urlLower, "sha256"):
		algorithm = "sha256"
		validator = IsValidSHA256
	case strings.Contains(urlLower, "sha512"):
		algorithm = "sha512"
		validator = IsValidSHA512
	}
	if hashPattern != "" {
		hash, detected, err := ParseChecksumPattern(body, hashPattern)
		if err != nil {
			return "", "", err
		}
		if algorithm != "" && algorithm != detected {
			return "", "", fmt.Errorf("hash-pattern returned %s digest for %s URL", detected, algorithm)
		}
		return hash, detected, nil
	}
	return ParseChecksumFile(body, targetFile, algorithm, validator)
}

// ParseChecksumPattern extracts and validates the digest in the first capture
// group of pattern. SHA-256 and SHA-512 are inferred from the captured value.
func ParseChecksumPattern(content, pattern string) (string, string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", "", fmt.Errorf("invalid hash-pattern: %w", err)
	}
	m := re.FindStringSubmatch(content)
	if len(m) < 2 {
		return "", "", fmt.Errorf("hash-pattern did not match with a capture group")
	}
	hash := strings.ToLower(m[1])
	switch {
	case IsValidSHA256(hash):
		return hash, "sha256", nil
	case IsValidSHA512(hash):
		return hash, "sha512", nil
	default:
		return "", "", fmt.Errorf("hash-pattern captured an invalid SHA-256/SHA-512 digest")
	}
}

// FetchFileSize sends HEAD and returns Content-Length.
func FetchFileSize(ctx context.Context, url string) (int64, error) {
	resp, err := doRequest(ctx, http.MethodHead, url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.ContentLength, nil
}

// ParseChecksumFile extracts a hash for targetFile from common checksum-file
// formats: bare hash; `<hash> <file>`; or `<hash> *<file>` (BSD).
func ParseChecksumFile(content, targetFile, algorithm string, validator func(string) bool) (string, string, error) {
	content = strings.TrimSpace(content)
	lines := strings.Split(content, "\n")
	validate := func(h string) (bool, string) {
		if validator != nil {
			return validator(h), algorithm
		}
		if IsValidSHA256(h) {
			return true, "sha256"
		}
		if IsValidSHA512(h) {
			return true, "sha512"
		}
		return false, ""
	}

	// Single bare hash.
	if len(lines) == 1 && !strings.Contains(content, " ") {
		if ok, algo := validate(content); ok {
			return strings.ToLower(content), algo, nil
		}
	}
	// `<hash> <filename>` lines.
	for _, line := range lines {
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) < 2 {
			continue
		}
		hash := parts[0]
		fname := strings.TrimPrefix(strings.Join(parts[1:], " "), "*")
		if ok, algo := validate(hash); ok {
			if fname == targetFile || filepath.Base(fname) == targetFile {
				return strings.ToLower(hash), algo, nil
			}
		}
	}
	// Last resort: any line whose first field is a valid hash.
	for _, line := range lines {
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) >= 1 {
			if ok, algo := validate(parts[0]); ok {
				return strings.ToLower(parts[0]), algo, nil
			}
		}
	}
	return "", "", fmt.Errorf("could not parse checksum")
}

// IsValidSHA256 returns true for a 64-char lowercase/uppercase hex string.
func IsValidSHA256(s string) bool { return isHexN(s, 64) }

// IsValidSHA512 returns true for a 128-char hex string.
func IsValidSHA512(s string) bool { return isHexN(s, 128) }

func isHexN(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func fetchAndParse(ctx context.Context, url, targetFile, algorithm string, validator func(string) bool) (string, string, error) {
	body, err := httpReadAll(ctx, url)
	if err != nil {
		return "", "", err
	}
	return ParseChecksumFile(body, targetFile, algorithm, validator)
}

func httpReadAll(ctx context.Context, url string) (string, error) {
	resp, err := doRequest(ctx, http.MethodGet, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := readBodyLimited(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
