package checker

import (
	"strconv"
	"strings"

	"github.com/cristatus/bunny/internal/manifest"
)

// VersionParts holds parsed version components used for URL templating.
type VersionParts struct {
	Full  string
	Major string
	Minor string
	Patch string
	Parts []string // dot-separated parts of Full
}

// ParseVersion splits a dotted version string. Missing parts come back as "".
func ParseVersion(version string) VersionParts {
	parts := strings.Split(version, ".")
	v := VersionParts{Full: version, Parts: parts}
	if len(parts) > 0 {
		v.Major = parts[0]
	}
	if len(parts) > 1 {
		v.Minor = parts[1]
	}
	if len(parts) > 2 {
		v.Patch = parts[2]
	}
	return v
}

// ExpandTemplate substitutes {version}, {major}, {minor}, {patch}, {versionN}.
func ExpandTemplate(template, version string) string {
	if template == "" {
		return ""
	}
	v := ParseVersion(version)
	vars := map[string]string{"version": v.Full, "major": v.Major, "minor": v.Minor, "patch": v.Patch}
	for i, p := range v.Parts {
		vars["version"+strconv.Itoa(i)] = p
	}
	return manifest.Expand(template, vars)
}
