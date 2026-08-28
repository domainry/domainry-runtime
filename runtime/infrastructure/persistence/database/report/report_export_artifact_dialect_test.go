package report

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestReportExportArtifactLargeContentAcrossDialects(t *testing.T) {
	cases := []struct{ name, driver, dsnEnv string }{
		{name: "sqlite", driver: "sqlite"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{DatabaseDriver: test.driver, DatabaseDSN: os.Getenv(test.dsnEnv), IntegrationSecretKey: "report-export-large-content-test"}
			if test.driver == "sqlite" {
				cfg.DBPath = filepath.Join(t.TempDir(), "report-export-large.db")
			} else if cfg.DatabaseDSN == "" {
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			store, err := database.OpenContext(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}

			key := fmt.Sprintf("large-report-export-%s-%d", test.name, time.Now().UnixNano())
			content := []byte("id,value\n" + strings.Repeat("row,0123456789abcdef0123456789abcdef\n", 5000))
			digest := sha256.Sum256(content)
			artifact := validReportExportArtifact()
			artifact.WorkspaceID, artifact.RequesterUserID, artifact.IdempotencyKey = key, key, key
			artifact.Token = hex.EncodeToString(digest[:])
			artifact.Content, artifact.ContentSHA256, artifact.RowCount = content, hex.EncodeToString(digest[:]), 5000
			t.Cleanup(func() {
				_, _ = store.DB().ExecContext(t.Context(), "DELETE FROM "+store.TableIdentifier("report_export_artifacts")+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), key)
			})

			repository := NewReportExportArtifactStore(store)
			created, wasCreated, err := repository.CreateOrGetReportExportArtifact(t.Context(), artifact)
			if err != nil || !wasCreated {
				t.Fatalf("create large artifact=%#v created=%v err=%v", created, wasCreated, err)
			}
			read, found, err := repository.ReportExportArtifactByToken(t.Context(), key, artifact.Token)
			if err != nil || !found || read.AuditID != artifact.AuditID || read.ContentSHA256 != artifact.ContentSHA256 || string(read.Content) != string(content) {
				t.Fatalf("read large artifact bytes=%d found=%v audit=%q hash=%q err=%v", len(read.Content), found, read.AuditID, read.ContentSHA256, err)
			}
		})
	}
}
