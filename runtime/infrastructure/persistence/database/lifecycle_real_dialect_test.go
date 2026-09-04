package database_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/lifecyclesdkfixture"
	"github.com/jackc/pgx/v5"
)

type dialectLifecycleOwner struct{}

func (dialectLifecycleOwner) Owner(context.Context) string { return "record" }
func (dialectLifecycleOwner) Preview(context.Context, string, lifecyclemodel.PolicyVersion, time.Time) (lifecyclecontract.CleanupPreview, error) {
	return lifecyclecontract.CleanupPreview{}, nil
}
func (dialectLifecycleOwner) ProcessBatch(context.Context, lifecyclemodel.CleanupJob, lifecyclemodel.PolicyVersion, []lifecyclemodel.LegalHold, int) (lifecyclemodel.CleanupBatchResult, error) {
	return lifecyclemodel.CleanupBatchResult{Done: true}, nil
}
func (dialectLifecycleOwner) ResolveSubject(_ context.Context, _, _, identity string) (string, error) {
	return identity, nil
}
func (dialectLifecycleOwner) PreviewSubject(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{"rows":0}`), nil
}
func (dialectLifecycleOwner) ExportSubject(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (dialectLifecycleOwner) EraseSubject(context.Context, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (owner dialectLifecycleOwner) ExportSubjectForRequest(ctx context.Context, _ string, workspaceID, identity string) (json.RawMessage, error) {
	return owner.ExportSubject(ctx, workspaceID, identity)
}
func (owner dialectLifecycleOwner) EraseSubjectForRequest(ctx context.Context, _ string, workspaceID, identity string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return owner.EraseSubject(ctx, workspaceID, identity, holds)
}

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
			binding, err := lifecyclesdkfixture.Open(t.Context(), store, identity)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = binding.Close(context.Background()) })
			artifacts, err := binding.SubjectArtifacts(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			owner := dialectLifecycleOwner{}
			if err := binding.BindOwners(t.Context(), lifecyclesdk.OwnerExtensions{Executors: []lifecyclecontract.OwnerLifecycleExecutor{owner}, SubjectResolver: owner, SubjectHandlers: []lifecyclecontract.SubjectExecutionHandler{owner}, Artifacts: artifacts}); err != nil {
				t.Fatal(err)
			}
			principal := lifecycleaccess.Principal{Known: true, WorkspaceID: identity, UserID: "dialect-test", Permissions: map[string]struct{}{
				lifecyclesdk.ActionLifecyclePoliciesPublish: {},
				lifecyclesdk.ActionLifecyclePoliciesList:    {},
			}}
			bundle := &identitysdk.AccessBundle{Subject: identitysdk.Subject{WorkspaceID: identitysdk.WorkspaceID(identity), SubjectID: "dialect-test"}}
			for permission := range principal.Permissions {
				separator := strings.LastIndex(permission, ".")
				resource, action := permission[:separator], permission[separator+1:]
				bundle.FunctionGrants = append(bundle.FunctionGrants, identitysdk.FunctionGrant{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow})
				bundle.DataPolicies = append(bundle.DataPolicies, identitysdk.DataPolicy{Key: "dialect-" + permission, Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow, DataScopes: []identitysdk.DataScope{identitysdk.DataScopeAll}})
			}
			requestContext := identitysdk.WithRequestIdentity(t.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: identity, UserID: "dialect-test", AccessBundle: bundle}})
			policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: identity, Version: "1", Owner: "record", Class: lifecyclemodel.RetentionClassProduct, DefaultRetention: 24 * time.Hour, MinimumRetention: time.Hour, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete}}
			if _, err := binding.Governance().PublishPolicy(requestContext, policy, principal); err != nil {
				t.Fatal(err)
			}
			policies, err := binding.Governance().ListPolicies(requestContext, principal)
			if err != nil || len(policies) != 1 || policies[0].Policy.Key != identity {
				t.Fatalf("policies=%#v err=%v", policies, err)
			}
			t.Cleanup(func() {
				_, _ = store.DB().ExecContext(context.Background(), "DELETE FROM "+store.TableIdentifier("_lifecycle_policy_versions")+" WHERE "+store.Identifier("workspace_id")+"="+store.Placeholder(1), identity)
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
