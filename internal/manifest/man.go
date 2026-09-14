package manifest

import (
	"path/filepath"
	"regexp"
	"strings"
)

var manSectionPattern = regexp.MustCompile(`^[1-9][A-Za-z0-9]*$`)

// ManSection extracts the man section (e.g. "1", "3p", "8x") from a man
// page's filename, tolerating a trailing ".gz". Returns "" when the filename
// does not end in a recognizable section suffix.
func ManSection(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".gz")
	section := strings.TrimPrefix(filepath.Ext(base), ".")
	if manSectionPattern.MatchString(section) {
		return section
	}
	return ""
}
