package checker

import "strings"

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
// {versionN} is replaced first so it can't be eaten by a partial {version} match.
func ExpandTemplate(template, version string) string {
	if template == "" {
		return ""
	}
	v := ParseVersion(version)
	out := template
	for i, p := range v.Parts {
		out = strings.ReplaceAll(out, "{version"+string(rune('0'+i))+"}", p)
	}
	out = strings.ReplaceAll(out, "{version}", v.Full)
	out = strings.ReplaceAll(out, "{major}", v.Major)
	out = strings.ReplaceAll(out, "{minor}", v.Minor)
	out = strings.ReplaceAll(out, "{patch}", v.Patch)
	return out
}
