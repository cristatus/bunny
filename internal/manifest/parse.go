package manifest

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parse decodes a Manifest from YAML and validates it. Unknown fields are
// rejected so contributors get an immediate signal when a manifest carries
// stale schema (e.g. the old `paths:` or `sandbox:` blocks).
func Parse(r io.Reader) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	// A trailing document separator ("---") is not a second document. Scan
	// past any empty trailing documents; reject only a real second document.
	for {
		var extra any
		err := dec.Decode(&extra)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse manifest trailer: %w", err)
		}
		if extra != nil {
			return nil, fmt.Errorf("parse manifest: multiple YAML documents are not allowed")
		}
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ParseBytes is a convenience wrapper around Parse.
func ParseBytes(data []byte) (*Manifest, error) {
	return Parse(strings.NewReader(string(data)))
}
