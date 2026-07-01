package memory

import "path/filepath"

// RunBundlePath returns the L3 raw run bundle namespace for one project run.
// It is intentionally separate from archive/<project>, which stores curated L2
// memory history rather than raw workflow artifacts.
func RunBundlePath(project, runID string) string {
	return filepath.Join(VaultDir, "runs", project, runID)
}
