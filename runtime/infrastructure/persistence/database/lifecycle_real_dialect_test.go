package database_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/lifecyclesdkfixture"
	"github.com/jackc/pgx/v5"
)

// TestLifecyclePersistenceAcrossRealDialects is the deployment gate for the
// extracted module. SQLite always runs; CI can require PostgreSQL and MySQL by
// setting RUNTIME_REQUIRE_REAL_DIALECTS=1 with both existing Runtime DSNs.
func TestLifecyclePersistenceAcrossRealDialects(t *testing.T) {
	for _, test := range []struct{ name, driver, dsnEnv string }{
		{name: "sqlite", driver: "sqlite"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(test.dsnEnv))
			if test.dsnEnv != "" && dsn == "" {
				if os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS") == "1" {
					t.Fatalf("%s is required when RUNTIME_REQUIRE_REAL_DIALECTS=1", test.dsnEnv)
				}
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			identity := fmt.Sprintf("lifecycle_%s_%d", test.name, time.Now().UTC().UnixNano())
			cfg := config.Config{DatabaseDriver: test.driver, DatabaseDSN: dsn, IntegrationSecretKey: identity}
			if test.driver == "sqlite" {
				cfg.DBPath = filepath.Join(t.TempDir(), "lifecycle-dialect.db")
			}
			if test.driver == "pgx" {
				cfg.DatabaseSchema = identity
				cleanupPostgresLifecycleSchema(t, dsn, identity)
			}
			store, err := database.OpenContext(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.EnsureLifecycleSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			binding, err := lifecyclesdkfixture.Open(t.Context(), store, identity)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = binding.Close(context.Background()) })
			repository := binding.Repository()
			policy := lifecyclemodel.PolicyVersion{WorkspaceID: identity, Policy: lifecyclemodel.RetentionPolicy{Key: identity, Version: "1", Owner: "record"}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedAt: time.Now().UTC()}
			if err := repository.SavePolicy(t.Context(), policy); err != nil {
				t.Fatal(err)
			}
			policies, err := repository.ListPolicies(t.Context(), identity)
			if err != nil || len(policies) != 1 || policies[0].Policy.Key != identity {
				t.Fatalf("policies=%#v err=%v", policies, err)
			}
			t.Cleanup(func() {
				_, _ = store.DB().ExecContext(context.Background(), "DELETE FROM "+store.TableIdentifier("lifecycle_policy_versions")+" WHERE "+store.Identifier("workspace_id")+"="+store.Placeholder(1), identity)
			})
		})
	}
}

func cleanupPostgresLifecycleSchema(t *testing.T, dsn, schema string) {
	t.Helper()
	connection, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = connection.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = connection.Close(ctx)
	})
}
