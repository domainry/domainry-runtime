package runtime

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func bootstrapTestConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		AppLocale:             "en-US",
		IdentityWorkspaceID:   "workspace-primary",
		IdentityAudience:      "domainry-runtime",
		DatabaseDriver:        "sqlite",
		DBPath:                filepath.Join(t.TempDir(), "runtime.db"),
		UploadDir:             filepath.Join(t.TempDir(), "uploads"),
		HTTPShutdownTimeout:   time.Second,
		SchedulerLeaseTTL:     time.Minute,
		SchedulerPollInterval: 5 * time.Millisecond,
		SchedulerBatchSize:    5,
	}
}
