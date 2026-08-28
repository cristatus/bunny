package runtime

// cleanHomeArgs blanks HOME with an empty tmpfs, or nil when the policy is
// not clean, so callers can append it unconditionally. No lower layer means
// no seed to read and nothing for persist to bind back to.
func cleanHomeArgs(policy *PackageSandbox, isolatedHome string) []string {
	if policy.Home != "clean" {
		return nil
	}
	return []string{"--tmpfs", isolatedHome}
}
