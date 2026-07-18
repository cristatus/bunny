package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/manifest"
)

func init() { Register(&JSON{}) }

// JSON checks a JSON API and extracts the latest version via JSONPath-lite.
type JSON struct{}

func (j *JSON) Type() string { return "json" }

func (j *JSON) Check(ctx context.Context, cfg *manifest.UpdateConfig, currentVersion, sourceURL string) (*Result, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("json checker requires url")
	}
	if cfg.TagQuery == "" && cfg.VersionQuery == "" {
		return nil, fmt.Errorf("json checker requires tag-query or version-query")
	}

	body, err := httpReadAll(ctx, cfg.URL)
	if err != nil {
		return nil, err
	}
	var data any
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return nil, err
	}

	tag := ""
	if cfg.TagQuery != "" {
		tag, err = extractPath(data, cfg.TagQuery)
		if err != nil {
			return nil, fmt.Errorf("extract tag: %w", err)
		}
	}

	version := tag
	if cfg.VersionQuery != "" {
		q := strings.ReplaceAll(cfg.VersionQuery, "{tag}", tag)
		if q != tag && q != "" {
			version, err = extractPath(data, q)
			if err != nil {
				return nil, fmt.Errorf("extract version: %w", err)
			}
		}
	}
	if cfg.TagPattern != "" {
		if re, err := regexp.Compile(cfg.TagPattern); err == nil {
			if m := re.FindStringSubmatch(version); len(m) >= 2 {
				version = m[1]
			}
		}
	}
	log.Debug("JSON version", "version", version, "tag", tag)

	r := &Result{LatestVersion: version, HasUpdate: version != currentVersion}

	if cfg.URLQuery != "" {
		q := strings.ReplaceAll(cfg.URLQuery, "{tag}", tag)
		q = strings.ReplaceAll(q, "{version}", version)
		if u, err := extractPath(data, q); err == nil {
			r.DownloadURL = u
		}
	}
	if r.DownloadURL == "" && cfg.URLTemplate != "" {
		t := strings.ReplaceAll(cfg.URLTemplate, "{tag}", tag)
		r.DownloadURL = ExpandTemplate(t, version)
	}

	if r.DownloadURL != "" {
		target := filepath.Base(r.DownloadURL)
		if cfg.HashQuery != "" {
			q := strings.ReplaceAll(cfg.HashQuery, "{tag}", tag)
			q = strings.ReplaceAll(q, "{version}", version)
			if h, err := extractPath(data, q); err == nil && h != "" {
				r.Hash = h
				switch len(h) {
				case 64:
					r.HashAlgorithm = "sha256"
				case 128:
					r.HashAlgorithm = "sha512"
				}
			}
		}
		if r.Hash == "" && cfg.HashURL != "" {
			u := strings.ReplaceAll(cfg.HashURL, "{tag}", tag)
			u = ExpandTemplate(u, version)
			if h, a, err := FetchChecksumFromURL(ctx, u, target, cfg.HashPattern); err == nil {
				r.Hash = h
				r.HashAlgorithm = a
			}
		}
		if r.Hash == "" {
			if h, a, err := FetchChecksum(ctx, r.DownloadURL); err == nil {
				r.Hash = h
				r.HashAlgorithm = a
			}
		}
		if size, err := FetchFileSize(ctx, r.DownloadURL); err == nil && size > 0 {
			r.Size = size
		}
	}
	_ = sourceURL // currently unused; kept for parity with the Backend interface
	return r, nil
}

// extractPath supports dotted paths with optional array indexing and
// filter expressions: `field.sub`, `[0]`, `[].field`, `$[?(@.k=='v')].x`.
func extractPath(data any, path string) (string, error) {
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	cur := data

	for path != "" {
		if i := strings.Index(path, "["); i != -1 {
			if key := path[:i]; key != "" {
				m, ok := cur.(map[string]any)
				if !ok {
					return "", fmt.Errorf("expected object, got %T", cur)
				}
				cur = m[key]
			}
			end := matchingBracket(path, i)
			if end == -1 {
				return "", fmt.Errorf("unclosed bracket")
			}
			content := path[i+1 : end]
			if strings.HasPrefix(content, "?(") && strings.HasSuffix(content, ")") {
				v, err := applyFilter(cur, content[2:len(content)-1])
				if err != nil {
					return "", err
				}
				cur = v
			} else {
				idx, err := strconv.Atoi(content)
				if err != nil {
					return "", fmt.Errorf("invalid index %q", content)
				}
				arr, ok := cur.([]any)
				if !ok {
					return "", fmt.Errorf("expected array, got %T", cur)
				}
				if idx < 0 || idx >= len(arr) {
					return "", fmt.Errorf("index %d out of bounds", idx)
				}
				cur = arr[idx]
			}
			path = strings.TrimPrefix(path[end+1:], ".")
			continue
		}
		dot := strings.Index(path, ".")
		var key string
		if dot == -1 {
			key, path = path, ""
		} else {
			key, path = path[:dot], path[dot+1:]
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("expected object, got %T", cur)
		}
		cur = m[key]
	}

	switch v := cur.(type) {
	case string:
		return v, nil
	case float64:
		return fmt.Sprintf("%v", v), nil
	default:
		return "", fmt.Errorf("unexpected type %T", cur)
	}
}

func matchingBracket(s string, start int) int {
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func applyFilter(data any, expr string) (any, error) {
	arr, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("filter requires array, got %T", data)
	}
	expr = strings.TrimPrefix(expr, "@.")
	eq := strings.Index(expr, "==")
	if eq == -1 {
		return nil, fmt.Errorf("unsupported filter %q", expr)
	}
	field := expr[:eq]
	value := expr[eq+2:]
	isString := false
	if (strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) ||
		(strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)) {
		value = value[1 : len(value)-1]
		isString = true
	}
	for _, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		f := obj[field]
		if isString {
			if s, ok := f.(string); ok && s == value {
				return item, nil
			}
		} else {
			if n, ok := f.(float64); ok {
				if want, err := strconv.ParseFloat(value, 64); err == nil && n == want {
					return item, nil
				}
			}
			if b, ok := f.(bool); ok {
				if (value == "true" && b) || (value == "false" && !b) {
					return item, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("no element matches @.%s==%s", field, value)
}
