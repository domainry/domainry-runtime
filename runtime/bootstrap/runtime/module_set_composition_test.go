package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	dataexchangemodule "github.com/domainry/domainry-data-exchange/module"
	"github.com/domainry/domainry-foundation/modulecapability"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	integrationmodule "github.com/domainry/domainry-integration/module"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	monitoringremote "github.com/domainry/domainry-monitoring-sdk/remote"
	monitoringcapability "github.com/domainry/domainry-monitoring/capability"
	monitoringmodule "github.com/domainry/domainry-monitoring/module"
	monitoringserver "github.com/domainry/domainry-monitoring/saas"
	notificationmodule "github.com/domainry/domainry-notification/module"
	"github.com/domainry/domainry-orm/query"
	reportmodule "github.com/domainry/domainry-report/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	publicationstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
	workflowstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"golang.org/x/mod/modfile"
)

const moduleSetLockContractVersion = "domainry-runtime-module-set-v1"

type moduleSetLock struct {
	ContractVersion string                `json:"contract_version"`
	SetSHA256       string                `json:"set_sha256"`
	Modules         []moduleSetLockModule `json:"modules"`
}

type moduleSetLockModule struct {
	Key                      string `json:"key"`
	Implementation           string `json:"implementation"`
	ImplementationVersion    string `json:"implementation_version"`
	ImplementationSum        string `json:"implementation_sum"`
	SDK                      string `json:"sdk"`
	SDKVersion               string `json:"sdk_version"`
	SDKSum                   string `json:"sdk_sum"`
	CapabilityContractSHA256 string `json:"capability_contract_sha256"`
}

func TestPinnedModuleSetLockMatchesGoMod(t *testing.T) {
	lock := loadModuleSetLock(t)
	if lock.ContractVersion != moduleSetLockContractVersion || len(lock.Modules) != 11 {
		t.Fatalf("module-set lock contract=%q modules=%d", lock.ContractVersion, len(lock.Modules))
	}
	wantDigest := moduleSetLockSHA256(lock)
	if lock.SetSHA256 != wantDigest {
		t.Fatalf("module-set digest=%q want=%q", lock.SetSHA256, wantDigest)
	}

	root := moduleSetRepositoryRoot(t)
	rawGoMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := modfile.Parse("go.mod", rawGoMod, nil)
	if err != nil {
		t.Fatal(err)
	}
	required := make(map[string]string, len(parsed.Require))
	for _, item := range parsed.Require {
		if !item.Indirect {
			required[item.Mod.Path] = item.Mod.Version
		}
	}
	sums := readModuleSetGoSums(t, filepath.Join(root, "go.sum"))
	seenKeys := map[string]bool{}
	for _, module := range lock.Modules {
		if module.Key == "" || seenKeys[module.Key] {
			t.Fatalf("module-set lock has invalid or duplicate key %q", module.Key)
		}
		seenKeys[module.Key] = true
		for _, dependency := range []struct {
			kind, path, version, sum string
		}{
			{kind: "implementation", path: module.Implementation, version: module.ImplementationVersion, sum: module.ImplementationSum},
			{kind: "sdk", path: module.SDK, version: module.SDKVersion, sum: module.SDKSum},
		} {
			if required[dependency.path] != dependency.version {
				t.Errorf("%s %s go.mod version=%q lock=%q", module.Key, dependency.kind, required[dependency.path], dependency.version)
			}
			if sums[dependency.path+" "+dependency.version] != dependency.sum {
				t.Errorf("%s %s go.sum=%q lock=%q", module.Key, dependency.kind, sums[dependency.path+" "+dependency.version], dependency.sum)
			}
		}
	}
}

func TestPinnedModuleSetComposesAllElevenBindingsOnOneHostDatabase(t *testing.T) {
	for _, topology := range []struct {
		name       string
		monitoring func(*testing.T) monitoringsdk.Factory
		wantMode   string
	}{
		{name: "all-module", monitoring: moduleSetMonitoringModuleFactory, wantMode: "module"},
		{name: "monitoring-saas", monitoring: moduleSetMonitoringSaaSFactory, wantMode: "saas"},
	} {
		t.Run(topology.name, func(t *testing.T) {
			cfg := moduleSetTestConfig(t)
			firstDigests, firstLedgerRows := runPinnedModuleSet(t, cfg, topology.monitoring(t), topology.wantMode)
			secondDigests, secondLedgerRows := runPinnedModuleSet(t, cfg, topology.monitoring(t), topology.wantMode)
			if fmt.Sprint(secondDigests) != fmt.Sprint(firstDigests) {
				t.Fatalf("module contract digests changed after restart: first=%v second=%v", firstDigests, secondDigests)
			}
			if secondLedgerRows != firstLedgerRows {
				t.Fatalf("module migrations were reapplied after restart: first=%d second=%d", firstLedgerRows, secondLedgerRows)
			}
		})
	}
}

func TestPinnedModuleSetComposesAllElevenBindingsAcrossRealDialects(t *testing.T) {
	stamp := time.Now().UnixNano()
	tests := []struct {
		name   string
		dsnEnv string
		cfg    func(*testing.T, string) config.Config
	}{
		{name: "postgres", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN", cfg: func(t *testing.T, dsn string) config.Config {
			schema := fmt.Sprintf("runtime_module_set_%d", stamp)
			admin, err := pgx.Connect(t.Context(), dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				// Test infrastructure provisioning has no domainry-orm equivalent.
				_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
				_ = admin.Close(cleanupCtx)
			})
			return config.Config{DatabaseDriver: "postgres", DatabaseDSN: dsn, DatabaseSchema: schema, DatabaseMigrationMode: "apply"}
		}},
		{name: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN", cfg: func(t *testing.T, dsn string) config.Config {
			parsed, err := mysqldriver.ParseDSN(dsn)
			if err != nil {
				t.Fatal(err)
			}
			adminConfig := parsed.Clone()
			adminConfig.DBName = ""
			admin, err := sql.Open("mysql", adminConfig.FormatDSN())
			if err != nil {
				t.Fatal(err)
			}
			databaseName := fmt.Sprintf("runtime_module_set_%d", stamp)
			identifier := "`" + databaseName + "`"
			// Test infrastructure provisioning has no domainry-orm equivalent.
			if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+identifier+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
				_ = admin.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_, _ = admin.ExecContext(cleanupCtx, "DROP DATABASE IF EXISTS "+identifier)
				_ = admin.Close()
			})
			parsed.DBName = databaseName
			return config.Config{DatabaseDriver: "mysql", DatabaseDSN: parsed.FormatDSN(), DatabaseMigrationMode: "apply"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(test.dsnEnv))
			if dsn == "" {
				if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
					t.Fatalf("%s is required", test.dsnEnv)
				}
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			cfg := moduleSetTestConfig(t)
			database := test.cfg(t, dsn)
			cfg.DatabaseDriver, cfg.DatabaseDSN, cfg.DatabaseSchema = database.DatabaseDriver, database.DatabaseDSN, database.DatabaseSchema
			cfg.DatabaseMigrationMode, cfg.DBPath = database.DatabaseMigrationMode, ""
			firstDigests, firstLedgerRows := runPinnedModuleSet(t, cfg, moduleSetMonitoringModuleFactory(t), "module")
			secondDigests, secondLedgerRows := runPinnedModuleSet(t, cfg, moduleSetMonitoringModuleFactory(t), "module")
			if fmt.Sprint(secondDigests) != fmt.Sprint(firstDigests) || secondLedgerRows != firstLedgerRows {
				t.Fatalf("real-dialect restart drifted: first_digests=%v second_digests=%v first_ledger=%d second_ledger=%d", firstDigests, secondDigests, firstLedgerRows, secondLedgerRows)
			}
		})
	}
}

func TestRuntimeAgentRetentionAndSubjectErasureEndToEnd(t *testing.T) {
	cfg := moduleSetTestConfig(t)
	store, err := PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	identityBinding, err := identitymodule.NewFactory(identitymodule.Options{DatabaseDriver: cfg.DatabaseDriver, DatabasePath: cfg.DBPath}).OpenWithDatabase(
		t.Context(), identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)},
		identitysdk.DatabaseHandle{Pool: store.DB(), Driver: store.Driver(), Schema: store.DatabaseSchema(), FilePath: cfg.DBPath, Migrations: store, ModuleMigrations: store},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = identityBinding.Close(context.Background()) })
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	application := NewVerifiedProjectWithAllTopologyFactoriesAndDatabase(
		t.Context(), cfg, handlers, connectors, runtimehttp.RuntimeReleaseIdentity{}, deploymentapplication.RuntimeReleaseArtifactEvidence{},
		identityBinding, notificationmodule.NewFactory(notificationmodule.Options{}), moduleSetMonitoringModuleFactory(t), schedulermodule.NewFactory(schedulermodule.Options{}),
		dataexchangemodule.NewFactory(dataexchangemodule.Options{}), integrationmodule.NewFactory(), reportmodule.NewFactory(), store,
		agentmodule.NewFactory(agentmodule.Options{BaseURL: "http://127.0.0.1", APIKey: "subject-erasure-test", AgentID: 1}),
	)
	t.Cleanup(func() { _ = application.CloseContext(context.Background()) })
	conversationBinding, ok := application.agentBinding.(agentsdk.ConversationBinding)
	if !ok || conversationBinding.Conversations() == nil {
		t.Fatal("Runtime did not expose Agent conversations")
	}
	conversations := conversationBinding.Conversations()
	alice := agentsdk.ConversationAuthority{Known: true, RuntimeID: cfg.RuntimeInstanceID, WorkspaceID: cfg.IdentityWorkspaceID, UserID: "alice", RoleKey: "member"}
	bob := alice
	bob.UserID = "bob"
	aliceConversation, err := conversations.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "alice-lifecycle", Title: "Alice private"}, alice)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conversations.WriteMemory(t.Context(), agentsdk.ConversationMemoryWrite{ID: "prefs:alice.v1", Title: "Alice", Content: "private", Enabled: true}, alice); err != nil {
		t.Fatal(err)
	}
	bobConversation, err := conversations.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "bob-lifecycle", Title: "Bob private"}, bob)
	if err != nil {
		t.Fatal(err)
	}
	ownerKey := func(a agentsdk.ConversationAuthority) string {
		raw, _ := json.Marshal([]string{a.RuntimeID, a.WorkspaceID, a.UserID})
		return fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	seedOwnerCopies := func(a agentsdk.ConversationAuthority, suffix string) {
		t.Helper()
		owner, now := ownerKey(a), time.Now().UTC().UnixMilli()
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _agent_user_todos(owner_key,todo_id,batch_id,position,created_at,source_conversation_id,status,revision,payload_json) VALUES(?,?,?,?,?,?,?,?,?)`, owner, "todo_"+suffix, "batch_"+suffix, 1, now, "", "open", 1, `{"title":"`+suffix+` todo"}`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _agent_artifacts(owner_key,artifact_id,version,created_at,source_conversation_id,payload_json) VALUES(?,?,?,?,?,?)`, owner, "artifact_"+suffix, 1, now, "", `{"title":"`+suffix+` artifact"}`); err != nil {
			t.Fatal(err)
		}
	}
	for _, subject := range []string{alice.UserID, bob.UserID} {
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _identity_users (id,workspace_id,name,email,status,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`, subject, cfg.IdentityWorkspaceID, subject, subject+"@example.test", "active", 1, "now", "now"); err != nil {
			t.Fatal(err)
		}
	}
	seedOwnerCopies(alice, "alice")
	seedOwnerCopies(bob, "bob")
	dataExchangeJobs := map[string]dataexchangesdk.Job{}
	integrationManagement := application.integrationBinding.(integrationsdk.ManagementBinding).Management()
	integrationWebPush := application.integrationBinding.(integrationsdk.WebPushBinding).WebPushSubscriptions()
	for _, subject := range []string{alice.UserID, bob.UserID} {
		if _, err = integrationManagement.CreateAPIKey(t.Context(), cfg.IdentityWorkspaceID, "operator", integrationsdk.APIKeyInput{Name: subject + "@private.example.test", ActorID: subject, RoleKey: "member"}); err != nil {
			t.Fatal(err)
		}
		if _, err = integrationWebPush.Upsert(t.Context(), cfg.IdentityWorkspaceID, subject, "push-"+subject, integrationsdk.WebPushSubscriptionInput{Endpoint: "https://push.example.test/" + subject, P256DH: "private-key-" + subject, Auth: "private-auth-" + subject}); err != nil {
			t.Fatal(err)
		}
	}
	for _, subject := range []string{alice.UserID, bob.UserID} {
		job, replayed, err := application.dataExchangeBinding.SubmitImport(t.Context(), dataexchangesdk.ImportRequest{
			Scope: dataexchangesdk.Scope{WorkspaceID: cfg.IdentityWorkspaceID, ActorID: subject}, Provider: "records", ObjectKey: "contact",
			IdempotencyKey: "subject-private-upload", Filename: subject + ".csv", Source: strings.NewReader("email\n" + subject + "@private.example.test\n"),
		})
		if err != nil || replayed || job.ActorID != subject {
			t.Fatalf("subject upload job=%+v replayed=%v err=%v", job, replayed, err)
		}
		dataExchangeJobs[subject] = job
	}

	for _, subject := range []string{alice.UserID, bob.UserID} {
		if err := workflowstore.NewWorkflowWorkerStore(store).InsertExecution(t.Context(), cfg.IdentityWorkspaceID, workflowmodel.WorkflowExecution{
			ID: "workflow-" + subject, WorkflowKey: "private_copy", Status: "pending", ActorID: subject, Payload: map[string]any{"email": subject + "@private.example.test"},
			CreatedAt: "2026-09-14T00:00:00Z", UpdatedAt: "2026-09-14T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := publicationstore.NewPublicationStore(store).InsertOutbox(t.Context(), cfg.IdentityWorkspaceID, publicationmodel.Message{
			ID: "publication-" + subject, ConnectorKey: "email", Operation: "send", CreatedBy: subject, DedupKey: "private-" + subject,
			Payload: map[string]any{"email": subject + "@private.example.test", "_runtime_attachment_base64": base64.StdEncoding.EncodeToString([]byte("email\n" + subject + "@private.example.test\n"))},
		}); err != nil {
			t.Fatal(err)
		}
	}
	principal := func(user string, permissions ...string) lifecycleaccess.Principal {
		value := lifecycleaccess.Principal{Known: true, WorkspaceID: cfg.IdentityWorkspaceID, UserID: user, Permissions: map[string]struct{}{}}
		for _, permission := range permissions {
			value.Permissions[permission] = struct{}{}
		}
		return value
	}
	identityContext := func(ctx context.Context, value lifecycleaccess.Principal) context.Context {
		bundle := &identitysdk.AccessBundle{Subject: identitysdk.Subject{WorkspaceID: identitysdk.WorkspaceID(value.WorkspaceID), SubjectID: identitysdk.SubjectID(value.UserID), OrgID: "org-a", OrgScopeIDs: []string{"org-a"}}}
		for permission := range value.Permissions {
			separator := strings.LastIndex(permission, ".")
			resource, action := permission[:separator], permission[separator+1:]
			bundle.FunctionGrants = append(bundle.FunctionGrants, identitysdk.FunctionGrant{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow})
			bundle.DataPolicies = append(bundle.DataPolicies, identitysdk.DataPolicy{Key: "runtime-" + permission, Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow, DataScopes: []identitysdk.DataScope{identitysdk.DataScopeAll}})
		}
		return identitysdk.WithRequestIdentity(ctx, identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: value.WorkspaceID, UserID: value.UserID, AccessBundle: bundle}})
	}
	requester := principal("requester", lifecyclesdk.ActionLifecycleSubjectRequestsCreate, lifecyclesdk.ActionLifecycleSubjectRequestsVerify, lifecyclesdk.ActionLifecycleSubjectRequestsPreview)
	approver := principal("operator", lifecyclesdk.ActionLifecycleSubjectRequestsApprove, lifecyclesdk.ActionLifecycleSubjectRequestsExecute, lifecyclesdk.ActionLifecycleDeletionsReplay)
	governance := application.lifecycleBinding.Governance()
	request, err := governance.CreateSubjectRequest(identityContext(t.Context(), requester), lifecyclemodel.SubjectRequest{WorkspaceID: cfg.IdentityWorkspaceID, Kind: lifecyclemodel.SubjectRequestErase, SubjectType: "user", SubjectID: alice.UserID, Reason: "user deletion"}, requester)
	if err != nil {
		t.Fatal(err)
	}
	if request, err = governance.VerifySubjectRequest(identityContext(t.Context(), requester), cfg.IdentityWorkspaceID, request.ID, "mfa-test", requester); err != nil {
		t.Fatal(err)
	}
	if request, err = governance.PreviewSubjectRequest(identityContext(t.Context(), requester), cfg.IdentityWorkspaceID, request.ID, requester); err != nil {
		t.Fatal(err)
	}
	if request, err = governance.ApproveSubjectRequest(identityContext(t.Context(), approver), cfg.IdentityWorkspaceID, request.ID, approver); err != nil {
		t.Fatal(err)
	}
	if request, err = governance.ExecuteSubjectRequest(identityContext(t.Context(), approver), cfg.IdentityWorkspaceID, request.ID, approver); err != nil || request.Status != lifecyclemodel.SubjectRequestSucceeded {
		t.Fatalf("request=%+v err=%v", request, err)
	}

	for _, subject := range []string{alice.UserID, bob.UserID} {
		for _, table := range []string{"_workflow_executions", "_publication_outbox"} {
			id := "workflow-" + subject
			if table == "_publication_outbox" {
				id = "publication-" + subject
			}
			var payload string
			if err := store.DB().QueryRowContext(t.Context(), "SELECT payload_json FROM "+store.TableIdentifier(table)+" WHERE workspace_id=? AND id=?", cfg.IdentityWorkspaceID, id).Scan(&payload); err != nil {
				t.Fatal(err)
			}
			if subject == alice.UserID && payload != "{}" {
				t.Fatalf("Runtime did not clean %s Alice copy: %s", table, payload)
			}
			if subject == bob.UserID && !strings.Contains(payload, "bob@private.example.test") {
				t.Fatalf("Runtime changed %s Bob copy", table)
			}
		}
	}
	if _, err = conversations.Get(t.Context(), aliceConversation.ID, alice); err == nil {
		t.Fatal("Agent owner did not erase Alice conversation")
	}
	if _, err = conversations.Get(t.Context(), bobConversation.ID, bob); err != nil {
		t.Fatal("Agent owner crossed into Bob data", err)
	}
	for _, subject := range []string{alice.UserID, bob.UserID} {
		push, e := integrationWebPush.List(t.Context(), cfg.IdentityWorkspaceID, subject)
		want := 1
		if subject == alice.UserID {
			want = 0
		}
		if e != nil || len(push) != want {
			t.Fatalf("Integration push subject=%s count=%d want=%d err=%v", subject, len(push), want, e)
		}
	}
	keys, e := integrationManagement.ListAPIKeys(t.Context(), cfg.IdentityWorkspaceID)
	if e != nil || len(keys) != 1 || keys[0].ActorID != bob.UserID {
		t.Fatalf("Integration private credentials not scoped: %+v err=%v", keys, e)
	}
	for _, check := range []struct {
		subject string
		count   int
	}{{alice.UserID, 0}, {bob.UserID, 1}} {
		var count int
		if err = store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _data_exchange_job_chunks WHERE workspace_id=? AND job_id=?`, cfg.IdentityWorkspaceID, dataExchangeJobs[check.subject].ID).Scan(&count); err != nil || count != check.count {
			t.Fatalf("subject=%s Data Exchange chunks=%d want=%d err=%v", check.subject, count, check.count, err)
		}
	}
	if _, _, err = application.dataExchangeBinding.SubmitImport(t.Context(), dataexchangesdk.ImportRequest{Scope: dataexchangesdk.Scope{WorkspaceID: cfg.IdentityWorkspaceID, ActorID: alice.UserID}, Provider: "records", ObjectKey: "contact", IdempotencyKey: "after-erasure", Source: strings.NewReader("email\nalice@private.example.test\n")}); err == nil {
		t.Fatal("erased subject recreated private upload")
	}
	for _, table := range []string{"_agent_user_todos", "_agent_artifacts"} {
		for _, check := range []struct {
			owner string
			want  int
		}{{ownerKey(alice), 0}, {ownerKey(bob), 1}} {
			var count int
			if err = store.DB().QueryRowContext(t.Context(), `SELECT count(*) FROM `+table+` WHERE owner_key=?`, check.owner).Scan(&count); err != nil || count != check.want {
				t.Fatalf("%s owner=%s count=%d want=%d err=%v", table, check.owner, count, check.want, err)
			}
		}
	}
	owners := map[string]bool{}
	rows, err := store.DB().QueryContext(t.Context(), `SELECT owner FROM _lifecycle_subject_execution_steps WHERE workspace_id=? AND request_id=?`, cfg.IdentityWorkspaceID, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var owner string
		if err = rows.Scan(&owner); err != nil {
			t.Fatal(err)
		}
		owners[owner] = true
	}
	_ = rows.Close()
	for _, owner := range []string{"agent", "todo", "knowledge", "identity", "data_exchange", "lifecycle", "runtime_evidence", "integration"} {
		if !owners[owner] {
			t.Fatalf("missing completed owner step %q: %v", owner, owners)
		}
	}
	if replayed, err := governance.ReplayRegisteredDeletions(identityContext(t.Context(), approver), cfg.IdentityWorkspaceID, 10, approver); err != nil || replayed != 1 {
		t.Fatalf("replayed=%d err=%v", replayed, err)
	}

	retained := alice
	retained.UserID = "retained-user"
	retainedConversation, err := conversations.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "retention-policy", Title: "Archived conversation"}, retained)
	if err != nil {
		t.Fatal(err)
	}
	archived := true
	retainedConversation, err = conversations.Update(t.Context(), retainedConversation.ID, agentsdk.ConversationUpdate{ExpectedRevision: retainedConversation.Revision, Archived: &archived}, retained)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Millisecond)
	retainedConversation.UpdatedAt = old
	conversationPayload, _ := json.Marshal(retainedConversation)
	if _, err = store.DB().ExecContext(t.Context(), `UPDATE _agent_conversations SET updated_at=?, payload_json=? WHERE owner_key=? AND conversation_id=?`, old.UnixMilli(), conversationPayload, ownerKey(retained), retainedConversation.ID); err != nil {
		t.Fatal(err)
	}
	retainedRunID := "retention-run"
	messagePayload := []byte(`{"id":"retention-message","run_id":"retention-run","role":"user","content":"retained user input"}`)
	if _, err = store.DB().ExecContext(t.Context(), `INSERT INTO _agent_conversation_messages(owner_key,conversation_id,message_id,run_id,seq,payload_json) VALUES(?,?,?,?,?,?)`, ownerKey(retained), retainedConversation.ID, "retention-message", retainedRunID, 1, messagePayload); err != nil {
		t.Fatal(err)
	}
	externalResponse := []byte(`{"call":{"name":"web_fetch"},"result":{"content":"retained external response"}}`)
	if _, err = store.DB().ExecContext(t.Context(), `INSERT INTO _agent_conversation_tool_calls(owner_key,conversation_id,run_id,step_no,payload_json,call_key) VALUES(?,?,?,?,?,?)`, ownerKey(retained), retainedConversation.ID, retainedRunID, 1, externalResponse, strings.Repeat("e", 64)); err != nil {
		t.Fatal(err)
	}
	policyAdmin := principal("retention-admin", lifecyclesdk.ActionLifecyclePoliciesPublish, lifecyclesdk.ActionLifecycleCleanupPreview, lifecyclesdk.ActionLifecycleCleanupJobsCreate, lifecyclesdk.ActionLifecycleArchiveList)
	policy, err := governance.PublishPolicy(identityContext(t.Context(), policyAdmin), lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{
		Key: "agent.dialog.v1", Version: "2", Owner: "agent", Class: lifecyclemodel.RetentionClassProduct,
		DefaultRetention: 24 * time.Hour, MinimumRetention: time.Hour, StatusRetention: map[string]time.Duration{"archived": 24 * time.Hour},
		WorkspaceMayExtend: true, LegalHoldEligible: true, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete,
	}, ApprovalRef: "approval-h02", ChangePlanRef: "change-h02"}, policyAdmin)
	if err != nil || policy.Status != lifecyclemodel.PolicyStatusPublished || policy.WorkspaceID != cfg.IdentityWorkspaceID {
		t.Fatalf("policy=%+v err=%v", policy, err)
	}
	preview, err := governance.PreviewCleanup(identityContext(t.Context(), policyAdmin), cfg.IdentityWorkspaceID, policy.Policy.Key, policyAdmin, time.Now().UTC())
	if err != nil || preview.Rows < 1 || preview.Bytes == 0 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	job, err := governance.CreateCleanupJob(identityContext(t.Context(), policyAdmin), lifecyclemodel.CleanupJob{WorkspaceID: cfg.IdentityWorkspaceID, PolicyKey: policy.Policy.Key, Operation: lifecyclemodel.OperationPurge, Reason: "H02 retention acceptance"}, policyAdmin)
	if err != nil || job.EstimatedRows < 1 {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	worker := lifecycleaccess.NewSystemPrincipal("lifecycle-worker", lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeInstallation, "H02 retention acceptance"), lifecyclesdk.ActionLifecycleCleanupJobsProcess)
	job, err = governance.ProcessCleanupJob(t.Context(), cfg.IdentityWorkspaceID, job.ID, "h02-worker", time.Minute, 100, time.Now().UTC(), worker)
	if err != nil || job.Status != lifecyclemodel.CleanupStatusSucceeded || job.Archived < 1 || job.Purged < 1 {
		t.Fatalf("processed job=%+v err=%v", job, err)
	}
	if _, err = conversations.Get(t.Context(), retainedConversation.ID, retained); err == nil {
		t.Fatal("retention purge left the eligible archived conversation")
	}
	if _, err = conversations.Get(t.Context(), bobConversation.ID, bob); err != nil {
		t.Fatal("retention purge crossed into an active conversation", err)
	}
	entries, err := governance.ListArchiveEntries(identityContext(t.Context(), policyAdmin), "agent.conversation", 100, policyAdmin)
	if err != nil {
		t.Fatal(err)
	}
	foundArchive := false
	var archivedPayload []byte
	for _, entry := range entries {
		if entry.Owner == "agent" && entry.ResourceID == retainedConversation.ID {
			foundArchive = true
			if err = store.DB().QueryRowContext(t.Context(), `SELECT payload_json FROM _lifecycle_archive_entries WHERE workspace_id=? AND id=?`, cfg.IdentityWorkspaceID, entry.ID).Scan(&archivedPayload); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !foundArchive || !bytes.Contains(archivedPayload, []byte("retained user input")) || !bytes.Contains(archivedPayload, []byte("retained external response")) {
		t.Fatalf("complete conversation archive was not retained: %+v", entries)
	}
}

func runPinnedModuleSet(t *testing.T, cfg config.Config, monitoringFactory monitoringsdk.Factory, monitoringMode string) (map[string]string, int) {
	t.Helper()
	store, err := PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	identityBinding, err := identitymodule.NewFactory(identitymodule.Options{DatabaseDriver: cfg.DatabaseDriver, DatabasePath: cfg.DBPath}).OpenWithDatabase(
		t.Context(),
		identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)},
		identitysdk.DatabaseHandle{Pool: store.DB(), Driver: store.Driver(), Schema: store.DatabaseSchema(), FilePath: cfg.DBPath, Migrations: store, ModuleMigrations: store},
	)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()

	application := NewVerifiedProjectWithAllTopologyFactoriesAndDatabase(
		t.Context(), cfg, handlers, connectors,
		runtimehttp.RuntimeReleaseIdentity{}, deploymentapplication.RuntimeReleaseArtifactEvidence{},
		identityBinding,
		notificationmodule.NewFactory(notificationmodule.Options{}),
		monitoringFactory,
		schedulermodule.NewFactory(schedulermodule.Options{}),
		dataexchangemodule.NewFactory(dataexchangemodule.Options{}),
		integrationmodule.NewFactory(),
		reportmodule.NewFactory(),
		store,
		agentmodule.NewFactory(agentmodule.Options{BaseURL: "http://127.0.0.1", APIKey: "module-set-test", AgentID: 1}),
	)
	defer func() {
		if err := application.CloseContext(context.Background()); err != nil {
			t.Errorf("close composed Runtime: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Errorf("close host database: %v", err)
		}
	}()

	inventory, err := application.ModuleInventory()
	if err != nil {
		t.Fatal(err)
	}
	wantModes := map[string]string{
		"identity": "module", "metadata": "module", "audit": "module", "lifecycle": "module",
		"notification": "module", "integration": "module", "monitoring": monitoringMode,
		"scheduler": "module", "data_exchange": "module", "agent": "module", "report": "module",
	}
	if len(inventory.Modules) != len(wantModes) {
		t.Fatalf("module inventory has %d Bindings, want %d: %+v", len(inventory.Modules), len(wantModes), inventory.Modules)
	}
	for _, descriptor := range inventory.Modules {
		wantMode, found := wantModes[descriptor.Key]
		if !found {
			t.Fatalf("unexpected module descriptor %+v", descriptor)
		}
		if string(descriptor.Mode) != wantMode {
			t.Fatalf("module %s mode=%s want=%s", descriptor.Key, descriptor.Mode, wantMode)
		}
		delete(wantModes, descriptor.Key)
	}
	if len(wantModes) != 0 {
		t.Fatalf("missing module Bindings: %v", wantModes)
	}

	digests := moduleSetCapabilityDigests(t, application)
	t.Logf("composed module capability digests: %v", digests)
	assertSingleHostMigrationLedger(t, store)
	return digests, moduleMigrationLedgerRows(t, store)
}

func moduleSetCapabilityDigests(t *testing.T, application *Runtime) map[string]string {
	t.Helper()
	lock := loadModuleSetLock(t)
	expected := make(map[string]string, len(lock.Modules))
	for _, module := range lock.Modules {
		expected[module.Key] = module.CapabilityContractSHA256
	}
	bindings := map[string]any{
		"identity": application.identityBinding, "metadata": application.metadataBinding,
		"audit": application.auditBinding, "lifecycle": application.lifecycleBinding,
		"notification": application.notificationBinding, "integration": application.integrationBinding,
		"monitoring": application.monitoringBinding, "scheduler": application.schedulerBinding,
		"data_exchange": application.dataExchangeBinding, "agent": application.agentBinding,
		"report": application.reportBinding,
	}
	digests := make(map[string]string, len(bindings))
	for key, raw := range bindings {
		binding, ok := raw.(modulecapability.Binding)
		if !ok || binding == nil {
			t.Fatalf("%s Binding %T does not expose the module capability contract", key, raw)
		}
		summary, err := binding.CapabilitySummary(t.Context())
		if err != nil {
			t.Fatalf("load %s capability summary: %v", key, err)
		}
		if summary.Identity.Key != key || len(summary.Identity.ContractSHA256) != 64 {
			t.Fatalf("%s capability identity=%+v", key, summary.Identity)
		}
		if summary.Identity.ContractSHA256 != expected[key] {
			t.Errorf("%s capability contract digest=%q lock=%q", key, summary.Identity.ContractSHA256, expected[key])
		}
		digests[key] = summary.Identity.ContractSHA256
	}
	return digests
}

func assertSingleHostMigrationLedger(t *testing.T, store *persistence.RuntimeStore) {
	t.Helper()
	var rows *sql.Rows
	var err error
	switch store.Driver() {
	case "sqlite":
		// sqlite_master is the only authoritative SQLite schema inventory.
		rows, err = store.DB().QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE '%schema_migrations%' ORDER BY name`)
	case "postgres", "pgx":
		// information_schema has no domainry-orm equivalent for ledger discovery.
		rows, err = store.DB().QueryContext(t.Context(), `SELECT table_name FROM information_schema.tables WHERE table_schema=$1 AND table_name LIKE '%schema_migrations%' ORDER BY table_name`, store.DatabaseSchema())
	case "mysql":
		// information_schema has no domainry-orm equivalent for ledger discovery.
		rows, err = store.DB().QueryContext(t.Context(), `SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name LIKE '%schema_migrations%' ORDER BY table_name`)
	default:
		t.Fatalf("unsupported composition-gate database driver %q", store.Driver())
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ledgers []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		ledgers = append(ledgers, name)
	}
	if strings.Join(ledgers, ",") != "_schema_migrations" {
		t.Fatalf("migration ledgers=%v want only _schema_migrations", ledgers)
	}

	ownerQuery, ownerArgs, err := query.NewSelectBuilder(store.SQLRenderer, "_schema_migrations").
		Columns("kind").
		Distinct().
		Where(query.Like("kind", "module:%")).
		OrderBy(query.Ascending("kind")).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	ownerRows, err := store.DB().QueryContext(t.Context(), ownerQuery, ownerArgs...)
	if err != nil {
		t.Fatal(err)
	}
	defer ownerRows.Close()
	var owners []string
	for ownerRows.Next() {
		var kind string
		if err := ownerRows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		owners = append(owners, strings.TrimPrefix(kind, "module:"))
	}
	wantOwners := []string{"agent", "audit", "data_exchange", "identity", "integration", "lifecycle", "metadata", "notification", "report", "scheduler"}
	if fmt.Sprint(owners) != fmt.Sprint(wantOwners) {
		t.Fatalf("host migration owners=%v want=%v", owners, wantOwners)
	}
}

func moduleMigrationLedgerRows(t *testing.T, store *persistence.RuntimeStore) int {
	t.Helper()
	statement, args, err := query.NewSelectBuilder(store.SQLRenderer, "_schema_migrations").
		Projections(query.Project(query.CountAll())).
		Where(query.Like("kind", "module:%")).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB().QueryRowContext(t.Context(), statement, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func moduleSetTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := bootstrapTestConfig(t)
	cfg.RuntimeInstanceID = "runtime-module-set"
	raw, err := os.ReadFile(cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["agents"] = []any{map[string]any{
		"key": "composition_probe", "name": "Composition probe", "description": "Opens the Agent Binding for the Runtime composition gate.",
		"tools": []any{"createRecord"},
	}}
	manifest["roles"] = []any{
		map[string]any{"key": "headquarters_admin", "name": "Headquarters administrator", "permissions": []any{}, "audience": "user", "assignment_mode": "manual"},
		map[string]any{"key": "store_manager", "name": "Store manager", "permissions": []any{}, "audience": "user", "assignment_mode": "manual"},
		map[string]any{"key": "staff", "name": "Staff", "permissions": []any{}, "audience": "user", "assignment_mode": "manual"},
	}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ManifestPath = filepath.Join(t.TempDir(), "module-set-manifest.json")
	if err := os.WriteFile(cfg.ManifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func moduleSetMonitoringModuleFactory(t *testing.T) monitoringsdk.Factory {
	t.Helper()
	return monitoringmodule.NewFactory(monitoringmodule.Options{})
}

func moduleSetMonitoringSaaSFactory(t *testing.T) monitoringsdk.Factory {
	t.Helper()
	service, err := monitoringserver.New(monitoringserver.Options{BearerToken: "module-set-secret"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Routes())
	t.Cleanup(server.Close)
	source, err := monitoringcapability.Open(monitoringcapability.Inputs{})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := source.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return monitoringremote.NewFactory(monitoringremote.Config{
		Endpoint: server.URL, Token: "module-set-secret", Client: server.Client(),
		CapabilityContractSHA256: summary.Identity.ContractSHA256,
	})
}

func loadModuleSetLock(t *testing.T) moduleSetLock {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleSetRepositoryRoot(t), "config", "runtime-module-set.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock moduleSetLock
	if err := json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	return lock
}

func moduleSetRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("resolve module-set test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
}

func readModuleSetGoSums(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || strings.HasSuffix(fields[1], "/go.mod") {
			continue
		}
		values[fields[0]+" "+fields[1]] = fields[2]
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func moduleSetLockSHA256(lock moduleSetLock) string {
	modules := append([]moduleSetLockModule(nil), lock.Modules...)
	sort.Slice(modules, func(i, j int) bool { return modules[i].Key < modules[j].Key })
	digest := sha256.New()
	_, _ = fmt.Fprintln(digest, lock.ContractVersion)
	for _, module := range modules {
		_, _ = fmt.Fprintf(digest, "%s\n%s@%s#%s\n%s@%s#%s\n%s\n",
			module.Key,
			module.Implementation, module.ImplementationVersion, module.ImplementationSum,
			module.SDK, module.SDKVersion, module.SDKSum,
			module.CapabilityContractSHA256,
		)
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}
