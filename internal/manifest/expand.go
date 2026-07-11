package manifest

import (
	"strings"
)

// Expand substitutes `{key}` placeholders in s using vars. Unknown
// placeholders pass through unchanged.
func Expand(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}
