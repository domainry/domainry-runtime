package projectdatabase

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) EnsureProjectDatabase(_ context.Context, cfg config.Config) error {
	path := strings.TrimSpace(cfg.DBPath)
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	return os.MkdirAll(filepath.Dir(path), 0o755)
}
