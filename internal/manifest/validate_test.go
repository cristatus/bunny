package manifest

import "testing"

// A typo'd variable name is silence in both directions — one that keeps
// crossing, or one that stops — so the policy fails at load instead.
func TestValidateSandboxEnvNames(t *testing.T) {
	keep := []string{"AWS_PROFILE", "GH_*", "_UNDERSCORE"}
	if err := ValidateSandboxPolicy("sandbox", &SandboxPolicy{Env: &SandboxEnv{Keep: &keep, Hide: []string{"TOKEN"}}}); err != nil {
		t.Fatalf("valid names rejected: %v", err)
	}
	for _, bad := range []string{"AWS PROFILE", "aws-profile", "*", "A*B", "", "$HOME"} {
		policy := &SandboxPolicy{Env: &SandboxEnv{Hide: []string{bad}}}
		if err := ValidateSandboxPolicy("sandbox", policy); err == nil {
			t.Errorf("invalid name %q accepted", bad)
		}
	}
}
