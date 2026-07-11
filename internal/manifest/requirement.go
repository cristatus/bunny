package manifest

import "strconv"

// ParseRequirement splits a `requires:` entry into its capability and an
// optional minimum major version:
//
//	"jdk"     → ("jdk", 0, false)        // any provider
//	"jdk-21"  → ("jdk-21", 0, false)     // concrete id
//	"jdk>=17" → ("jdk", 17, true)        // minimum major
//
// A `>=` entry whose suffix is not a positive integer (or whose capability is
// empty) returns hasMin=true with minMajor=0 / empty capability, so validation
// can reject it.
func ParseRequirement(req string) (capability string, minMajor int, hasMin bool) {
	const op = ">="
	i := indexOf(req, op)
	if i < 0 {
		return req, 0, false
	}
	capability = req[:i]
	if n, err := strconv.Atoi(req[i+len(op):]); err == nil && n > 0 {
		minMajor = n
	}
	return capability, minMajor, true
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
