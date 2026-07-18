package installer

// ProgressHook receives install progress for a single package: phase
// transitions (downloading/extracting/installing) and, during download,
// cumulative byte counts. Callers pass nil to opt out; the installer
// substitutes a no-op so call sites never need nil checks.
type ProgressHook interface {
	Phase(name string)
	Download(done, total int64)
}

type noopHook struct{}

func (noopHook) Phase(string)          {}
func (noopHook) Download(int64, int64) {}
