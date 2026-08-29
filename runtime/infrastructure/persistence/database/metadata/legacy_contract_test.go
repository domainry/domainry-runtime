package metadata

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	"encoding/json"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"path/filepath"
	"testing"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	changeplanpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/changeplan"
	transactionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/transaction"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"time"
)

type metadataTestStore struct {
	MetadataStore
	raw *RuntimeStore
}

func openStoreForMetadataTest(t *testing.T) *metadataTestStore {
	t.Helper()
	tempDir := t.TempDir()
	store, err := OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(tempDir, "app.db"),
	})
	if err != nil {
		t.Fatalf("open store for metadata test: %v", err)
	}
	if err := store.EnsureMetadataSchema(t.Context()); err != nil {
		t.Fatalf("ensure metadata schema: %v", err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure runtime schema: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &metadataTestStore{MetadataStore: NewMetadataStore(store), raw: store}
}

func TestPublishDefinitionCommitsDefinitionVersionAuditAndActiveRevision(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	audit := auditmodel.AuditEvent{ID: "audit-metadata-publish", WorkspaceID: "default", Event: "metadata_definition.saved", ObjectKey: "object", RecordID: "account", ActorID: "admin", RoleKey: "admin", Summary: "Saved object account", CreatedAt: "2026-07-19T00:00:00Z"}
	definition, err := store.PublishDefinition(t.Context(), metadataTestInstallationScope(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)}, audit)
	if err != nil {
		t.Fatal(err)
	}
	var activeCount, versionCount, auditCount, revisionCount, intentCount int
	queries := []struct {
		query string
		args  []any
		out   *int
	}{
		{`SELECT COUNT(*) FROM object_definitions WHERE resource_key = ? AND schema_hash = ?`, []any{"account", definition.SchemaHash}, &activeCount},
		{`SELECT COUNT(*) FROM metadata_definition_versions WHERE resource_type = 'object' AND resource_key = ? AND schema_hash = ?`, []any{"account", definition.SchemaHash}, &versionCount},
		{`SELECT COUNT(*) FROM _audit_events WHERE id = ?`, []any{audit.ID}, &auditCount},
		{`SELECT COUNT(*) FROM metadata_catalog WHERE key = 'schema_hash' AND value <> ''`, nil, &revisionCount},
		{`SELECT COUNT(*) FROM transaction_boundary_intents WHERE owner = 'metadata' AND operation = 'runtime_refresh' AND resource_id = 'object:account' AND status = 'executing'`, nil, &intentCount},
	}
	for _, item := range queries {
		if err := store.raw.DB().QueryRowContext(t.Context(), item.query, item.args...).Scan(item.out); err != nil {
			t.Fatal(err)
		}
	}
	if activeCount != 1 || versionCount != 1 || auditCount != 1 || revisionCount != 1 || intentCount != 1 {
		t.Fatalf("publication facts active=%d version=%d audit=%d revision=%d intent=%d", activeCount, versionCount, auditCount, revisionCount, intentCount)
	}
}

func TestPublishDefinitionReplayDoesNotDuplicateVersionAuditOrRefreshIntent(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	expectAbsent := ""
	request := metadatamodel.MetadataDefinitionUpsertRequest{SourceKind: "builder_v4", SourceID: "task-1:create-account", ExpectedSchemaHash: &expectAbsent, Payload: json.RawMessage(`{"key":"account","name":"Account"}`)}
	firstAudit := auditmodel.AuditEvent{ID: "audit-first", WorkspaceID: "default", Event: "metadata_definition.saved", ObjectKey: "object", RecordID: "account", CreatedAt: "2026-07-21T00:00:00Z"}
	first, err := store.PublishDefinition(t.Context(), metadataTestInstallationScope(), "object", "account", request, firstAudit)
	if err != nil {
		t.Fatal(err)
	}
	replayAudit := firstAudit
	replayAudit.ID = "audit-replay"
	replayed, err := store.PublishDefinition(t.Context(), metadataTestInstallationScope(), "object", "account", request, replayAudit)
	if err != nil || replayed.SchemaVersion != first.SchemaVersion || replayed.SchemaHash != first.SchemaHash {
		t.Fatalf("replayed=%#v first=%#v err=%v", replayed, first, err)
	}
	var versions, audits, intents int
	for _, query := range []struct {
		statement string
		out       *int
	}{
		{`SELECT COUNT(*) FROM metadata_definition_versions WHERE resource_type = 'object' AND resource_key = 'account'`, &versions},
		{`SELECT COUNT(*) FROM _audit_events WHERE id IN ('audit-first', 'audit-replay')`, &audits},
		{`SELECT COUNT(*) FROM transaction_boundary_intents WHERE owner = 'metadata' AND operation = 'runtime_refresh' AND resource_id = 'object:account'`, &intents},
	} {
		if err := store.raw.DB().QueryRowContext(t.Context(), query.statement).Scan(query.out); err != nil {
			t.Fatal(err)
		}
	}
	if versions != 1 || audits != 1 || intents != 1 {
		t.Fatalf("replay side effects versions=%d audits=%d intents=%d", versions, audits, intents)
	}
}

func TestPublishDefinitionRollsBackAllFactsWhenActiveRevisionFails(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	if _, err := store.raw.DB().Exec(`CREATE TRIGGER fail_metadata_active_revision BEFORE INSERT ON metadata_catalog WHEN NEW.key = 'schema_hash' BEGIN SELECT RAISE(FAIL, 'injected active revision failure'); END`); err != nil {
		t.Fatal(err)
	}
	audit := auditmodel.AuditEvent{ID: "audit-metadata-rollback", WorkspaceID: "default", Event: "metadata_definition.saved", ObjectKey: "object", RecordID: "account", ActorID: "admin", RoleKey: "admin", Summary: "Saved object account", CreatedAt: "2026-07-19T00:00:00Z"}
	if _, err := store.PublishDefinition(t.Context(), metadataTestInstallationScope(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)}, audit); err == nil {
		t.Fatal("expected injected active revision failure")
	}
	for table, predicate := range map[string]string{
		"object_definitions":           "resource_key = 'account'",
		"metadata_definition_versions": "resource_key = 'account'",
		"_audit_events":                "id = 'audit-metadata-rollback'",
		"transaction_boundary_intents": "resource_id = 'object:account'",
	} {
		var count int
		if err := store.raw.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE "+predicate).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("partial metadata publication persisted in %s", table)
		}
	}
}

func TestMetadataRefreshIntentCanBeReconciledAfterInlineFailure(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	audit := auditmodel.AuditEvent{ID: "audit-metadata-reconcile", WorkspaceID: "default", Event: "metadata_definition.saved", ObjectKey: "object", RecordID: "account", ActorID: "admin", RoleKey: "admin", Summary: "Saved object account", CreatedAt: "2026-07-19T00:00:00Z"}
	definition, err := store.PublishDefinition(t.Context(), metadataTestInstallationScope(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)}, audit)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteDefinitionRefresh(t.Context(), metadataTestInstallationScope(), "object", "account", definition.SchemaHash, "runtime refresh unavailable"); err != nil {
		t.Fatal(err)
	}
	intentID := metadataDefinitionRefreshIntentID("object", "account", definition.SchemaHash)
	intents := transactionpersistence.NewBoundaryIntentStore(store.raw)
	claimed, ok, err := intents.ClaimBoundaryIntent(t.Context(), "default", intentID, "metadata-reconciler", time.Now().UTC().Add(2*time.Minute).Format(time.RFC3339Nano))
	if err != nil || !ok || claimed.Status != transactionmodel.BoundaryIntentExecuting || claimed.AttemptCount != 1 {
		t.Fatalf("claimed=%+v ok=%v err=%v", claimed, ok, err)
	}
	completed, err := intents.TransitionBoundaryIntent(t.Context(), claimed.WorkspaceID, intentID, claimed.LeaseOwner, claimed.FencingToken, transactionmodel.BoundaryIntentSucceeded, "", "")
	if err != nil || completed.Status != transactionmodel.BoundaryIntentSucceeded {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
}

func TestMetadataFieldUpsertEndToEnd(t *testing.T) {
	store := openStoreForMetadataTest(t)

	// Bootstrap metadata tables (normally done by app startup via EnsureManifestMetadata).
	bootstrapManifest := manifestmodel.ManifestSchema{
		TemplateID: "test",
		Version:    "0.0.0",
		Objects: []definitionmodel.ObjectSchema{
			{Key: "e2e_item", Name: "E2E Item"},
		},
	}
	if err := store.EnsureManifestMetadata(t.Context(), bootstrapManifest); err != nil {
		t.Fatalf("EnsureManifestMetadata: %v", err)
	}

	// Ensure there is at least one object to work with.
	objPayload := map[string]any{"key": "e2e_item", "name": "E2E Item"}
	objJSON, _ := json.Marshal(objPayload)
	if _, err := store.UpsertMetadataDefinition(t.Context(), "object", "e2e_item", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(objJSON)}); err != nil {
		t.Fatalf("UpsertMetadataDefinition object: %v", err)
	}

	// Step 1: Upsert a new field definition.
	fieldPayload := map[string]any{
		"key":  "test_notes",
		"name": "Test Notes",
		"type": "text",
	}
	payloadJSON, _ := json.Marshal(fieldPayload)
	req := metadatamodel.MetadataDefinitionUpsertRequest{
		Payload:   json.RawMessage(payloadJSON),
		ObjectKey: "e2e_item",
	}
	def, err := store.UpsertMetadataDefinition(t.Context(), "field", "e2e_item.test_notes", req)
	if err != nil {
		t.Fatalf("UpsertMetadataDefinition field: %v", err)
	}
	if def.ResourceKey == "" {
		t.Fatalf("expected non-empty resource_key after upsert")
	}

	// Step 2: Reload manifest and sync storage so column is added.
	manifest, err := store.LoadManifestMetadata(t.Context())
	if err != nil {
		t.Fatalf("LoadManifestMetadata: %v", err)
	}
	if err := store.SyncManifestStorage(t.Context(), manifest); err != nil {
		t.Fatalf("SyncManifestStorage: %v", err)
	}
	plan, err := store.MetadataMigrationPlan(t.Context(), manifest)
	if err != nil {
		t.Fatalf("MetadataMigrationPlan: %v", err)
	}
	if len(plan) != 0 {
		t.Fatalf("expected synced object table to have no migration steps, got %#v", plan)
	}

	// Step 3: Confirm the field appears in the reloaded manifest.
	found := false
	for _, obj := range manifest.Objects {
		if obj.Key != "e2e_item" {
			continue
		}
		for _, f := range obj.Fields {
			if f.Key == "test_notes" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("field test_notes not found in reloaded manifest for object e2e_item")
	}
}

func TestMetadataDefinitionUpsertUsesExpectedSchemaHash(t *testing.T) {
	store := openStoreForMetadataTest(t)
	manifest := manifestmodel.ManifestSchema{TemplateID: "lock", Version: "1", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}}
	if err := store.EnsureManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	expectAbsent := ""
	created, err := store.UpsertMetadataDefinition(t.Context(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{ExpectedSchemaHash: &expectAbsent, Payload: json.RawMessage(`{"key":"account","name":"Account"}`)})
	if err != nil {
		t.Fatal(err)
	}
	expected := created.SchemaHash
	updated, err := store.UpsertMetadataDefinition(t.Context(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{ExpectedSchemaHash: &expected, Payload: json.RawMessage(`{"key":"account","name":"Business Account"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.SchemaHash == created.SchemaHash {
		t.Fatalf("expected content hash to change: before=%s after=%s", created.SchemaHash, updated.SchemaHash)
	}
	_, err = store.UpsertMetadataDefinition(t.Context(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{ExpectedSchemaHash: &expected, Payload: json.RawMessage(`{"key":"account","name":"Stale Update"}`)})
	conflict, ok := err.(*metadatamodel.MetadataDefinitionConflictError)
	if !ok || conflict.ExpectedHash != expected || conflict.CurrentHash != updated.SchemaHash {
		t.Fatalf("expected structured optimistic-lock conflict, got %#v", err)
	}
	current, found, err := store.GetMetadataDefinition(t.Context(), "object", "account")
	if err != nil || !found || current.SchemaHash != updated.SchemaHash {
		t.Fatalf("stale update changed current definition: current=%#v err=%v", current, err)
	}
}

func TestMetadataDefinitionMutationBatchRollsBackOnVersionConflict(t *testing.T) {
	store := openStoreForMetadataTest(t)
	manifest := manifestmodel.ManifestSchema{TemplateID: "batch", Version: "1", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}}
	if err := store.EnsureManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	first, err := store.UpsertMetadataDefinition(t.Context(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.UpsertMetadataDefinition(t.Context(), "object", "contact", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"contact","name":"Contact"}`)})
	if err != nil {
		t.Fatal(err)
	}
	firstHash, staleSecondHash := first.SchemaHash, "stale"
	draft, saved, err := changeplanpersistence.NewBusinessChangePlanStore(store.raw).SaveDraft(t.Context(), "default", changeplanmodel.BusinessChangePlanDraft{PlanID: "plan-atomic", Payload: json.RawMessage(`{"plan_id":"plan-atomic"}`), CreatedBy: "admin", UpdatedBy: "admin", CreatedAt: "2026-07-12T00:00:00Z", UpdatedAt: "2026-07-12T00:00:00Z"}, 0)
	if err != nil || !saved || draft.Revision != 1 {
		t.Fatalf("save change plan draft: draft=%#v saved=%v err=%v", draft, saved, err)
	}
	repository := changeplanpersistence.NewBusinessChangePlanStore(store.raw)
	inReview, ok, err := repository.TransitionDraft(t.Context(), "default", draft.PlanID, draft.Revision, "draft", "in_review", "reviewer", "2026-07-12T00:00:10Z")
	if err != nil || !ok {
		t.Fatalf("review change plan draft: draft=%#v ok=%v err=%v", inReview, ok, err)
	}
	draft, ok, err = repository.TransitionDraft(t.Context(), "default", draft.PlanID, inReview.Revision, "in_review", "approved", "approver", "2026-07-12T00:00:20Z")
	if err != nil || !ok {
		t.Fatalf("approve change plan draft: draft=%#v ok=%v err=%v", draft, ok, err)
	}
	_, err = store.ApplyDefinitionMutations(t.Context(), metadataTestInstallationScope(), []metadatamodel.MetadataDefinitionMutation{
		{Operation: "update", ResourceType: "object", ResourceKey: "account", Request: metadatamodel.MetadataDefinitionUpsertRequest{ExpectedSchemaHash: &firstHash, Payload: json.RawMessage(`{"key":"account","name":"Updated Account"}`)}},
		{Operation: "update", ResourceType: "object", ResourceKey: "contact", Request: metadatamodel.MetadataDefinitionUpsertRequest{ExpectedSchemaHash: &staleSecondHash, Payload: json.RawMessage(`{"key":"contact","name":"Updated Contact"}`)}},
	}, nil, &changeplanmodel.BusinessChangePlanPublication{WorkspaceID: "default", PlanID: draft.PlanID, ExpectedRevision: draft.Revision, UpdatedBy: "admin", UpdatedAt: "2026-07-12T00:01:00Z"})
	if _, ok := err.(*metadatamodel.MetadataDefinitionConflictError); !ok {
		t.Fatalf("expected batch conflict, got %v", err)
	}
	currentFirst, _, _ := store.GetMetadataDefinition(t.Context(), "object", "account")
	currentSecond, _, _ := store.GetMetadataDefinition(t.Context(), "object", "contact")
	if currentFirst.SchemaHash != first.SchemaHash || currentSecond.SchemaHash != second.SchemaHash {
		t.Fatalf("batch was partially applied: first=%#v second=%#v", currentFirst, currentSecond)
	}
	currentDraft, found, draftErr := changeplanpersistence.NewBusinessChangePlanStore(store.raw).GetDraft(t.Context(), "default", draft.PlanID)
	if draftErr != nil || !found || currentDraft.Status != "approved" || currentDraft.Revision != draft.Revision {
		t.Fatalf("failed metadata publication froze the plan: draft=%#v found=%v err=%v", currentDraft, found, draftErr)
	}
}

func TestMetadataChangePlanUsesApplyingStateUntilRuntimeFinalizes(t *testing.T) {
	store := openStoreForMetadataTest(t)
	if err := store.raw.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	draft, saved, err := changeplanpersistence.NewBusinessChangePlanStore(store.raw).SaveDraft(t.Context(), "default", changeplanmodel.BusinessChangePlanDraft{PlanID: "plan-two-phase", Payload: json.RawMessage(`{"plan_id":"plan-two-phase"}`), CreatedBy: "admin", UpdatedBy: "admin", CreatedAt: "2026-07-12T00:00:00Z", UpdatedAt: "2026-07-12T00:00:00Z"}, 0)
	if err != nil || !saved {
		t.Fatalf("save draft: %#v saved=%v err=%v", draft, saved, err)
	}
	repository := changeplanpersistence.NewBusinessChangePlanStore(store.raw)
	inReview, ok, err := repository.TransitionDraft(t.Context(), "default", draft.PlanID, draft.Revision, "draft", "in_review", "reviewer", "2026-07-12T00:00:10Z")
	if err != nil || !ok {
		t.Fatalf("review draft: %#v ok=%v err=%v", inReview, ok, err)
	}
	draft, ok, err = repository.TransitionDraft(t.Context(), "default", draft.PlanID, inReview.Revision, "in_review", "approved", "approver", "2026-07-12T00:00:20Z")
	if err != nil || !ok {
		t.Fatalf("approve draft: %#v ok=%v err=%v", draft, ok, err)
	}
	definitions, err := store.ApplyDefinitionMutations(t.Context(), metadataTestInstallationScope(), []metadatamodel.MetadataDefinitionMutation{{Operation: "create", ResourceType: "object", ResourceKey: "account", Request: metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)}}}, nil, &changeplanmodel.BusinessChangePlanPublication{WorkspaceID: "default", PlanID: draft.PlanID, ExpectedRevision: draft.Revision, UpdatedBy: "admin", UpdatedAt: "2026-07-12T00:01:00Z"})
	if err != nil || len(definitions) != 1 {
		t.Fatalf("apply metadata: %#v err=%v", definitions, err)
	}
	applying, found, err := changeplanpersistence.NewBusinessChangePlanStore(store.raw).GetDraft(t.Context(), "default", draft.PlanID)
	if err != nil || !found || applying.Status != "applying" || applying.Revision != draft.Revision+1 {
		t.Fatalf("expected recoverable applying state: %#v found=%v err=%v", applying, found, err)
	}
	finalized, published, err := changeplanpersistence.NewBusinessChangePlanStore(store.raw).PublishDraft(t.Context(), "default", draft.PlanID, applying.Revision, "admin", "2026-07-12T00:02:00Z")
	if err != nil || !published || finalized.Status != "published" || finalized.Revision != applying.Revision {
		t.Fatalf("finalize publication: %#v published=%v err=%v", finalized, published, err)
	}
}

func TestMetadataChangePlanAndRollbackSynchronizeLocalizedProjectionAtomically(t *testing.T) {
	store := openStoreForMetadataTest(t)
	if err := store.raw.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	firstPayload := json.RawMessage(`{"key":"account","name":"Account","i18n":{"en-US":{"name":"Account"},"zh-CN":{"name":"客户"}}}`)
	definitions, err := store.ApplyDefinitionMutations(t.Context(), metadataTestInstallationScope(), []metadatamodel.MetadataDefinitionMutation{{Operation: "create", ResourceType: "object", ResourceKey: "account", Request: metadatamodel.MetadataDefinitionUpsertRequest{Payload: firstPayload, SourceID: "plan-localized-v1"}}}, nil, nil)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("create localized definition: %#v err=%v", definitions, err)
	}
	assertLocalizedTextValue(t, store, "object", "account", "name", "zh-CN", "客户")
	first := definitions[0]
	secondPayload := json.RawMessage(`{"key":"account","name":"Account","i18n":{"en-US":{"name":"Customer Account"},"zh-CN":{"name":"客户账户"}}}`)
	definitions, err = store.ApplyDefinitionMutations(t.Context(), metadataTestInstallationScope(), []metadatamodel.MetadataDefinitionMutation{{Operation: "update", ResourceType: "object", ResourceKey: "account", Request: metadatamodel.MetadataDefinitionUpsertRequest{Payload: secondPayload, SourceID: "plan-localized-v2", ExpectedSchemaHash: &first.SchemaHash}}}, nil, nil)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("update localized definition: %#v err=%v", definitions, err)
	}
	assertLocalizedTextValue(t, store, "object", "account", "name", "zh-CN", "客户账户")
	second := definitions[0]
	rolledBack, err := store.RollbackMetadataDefinition(t.Context(), "object", "account", metadatamodel.MetadataDefinitionRollbackRequest{TargetVersion: first.SchemaVersion, ExpectedSchemaHash: second.SchemaHash, BusinessReason: "restore localization", ChangePlanID: "rollback-localized", BuilderTaskID: "builder-localized"}, auditmodel.AuditEvent{ID: "audit-localized", WorkspaceID: "default", Event: "metadata_definition.rolled_back", ActorID: "admin", CreatedAt: "2026-07-12T00:00:00Z"})
	if err != nil || rolledBack.SchemaHash != first.SchemaHash {
		t.Fatalf("rollback localized definition: %#v err=%v", rolledBack, err)
	}
	assertLocalizedTextValue(t, store, "object", "account", "name", "zh-CN", "客户")
}

func assertLocalizedTextValue(t *testing.T, store *metadataTestStore, entityType, entityKey, property, locale, expected string) {
	t.Helper()
	values, err := store.ListLocalizedTexts(t.Context(), principalmodel.InstallationWorkspaceID, metadatamodel.LocalizedTextQuery{WorkspaceID: principalmodel.InstallationWorkspaceID, EntityType: entityType, EntityKey: entityKey, Property: property, Locale: locale})
	if err != nil || len(values) != 1 || values[0].Text != expected {
		t.Fatalf("localized text %s/%s/%s/%s=%#v err=%v, want %q", entityType, entityKey, property, locale, values, err, expected)
	}
}

func TestMetadataRollbackCreatesNewVersionAndUsesCurrentHash(t *testing.T) {
	store := openStoreForMetadataTest(t)
	if err := store.raw.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{TemplateID: "rollback", Version: "1", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}}
	if err := store.EnsureManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	first, err := store.UpsertMetadataDefinition(t.Context(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.UpsertMetadataDefinition(t.Context(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Enterprise Account"}`)})
	if err != nil {
		t.Fatal(err)
	}
	request := metadatamodel.MetadataDefinitionRollbackRequest{TargetVersion: first.SchemaVersion, ExpectedSchemaHash: second.SchemaHash, ChangePlanID: "rollback-plan"}
	audit := auditmodel.AuditEvent{ID: "audit-rollback", WorkspaceID: "default", Event: "metadata_definition.rolled_back", ObjectKey: "object", RecordID: "account", ActorID: "admin", CreatedAt: "2026-07-11T00:00:00Z", Metadata: map[string]any{"change_plan_id": "rollback-plan"}}
	rolledBack, err := store.RollbackMetadataDefinition(t.Context(), "object", "account", request, audit)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.SchemaVersion == first.SchemaVersion || rolledBack.SchemaVersion == second.SchemaVersion || rolledBack.SchemaHash != first.SchemaHash || rolledBack.Name != "Account" {
		t.Fatalf("rollback did not create a new target-equivalent version: %#v", rolledBack)
	}
	versions, err := store.ListMetadataDefinitionVersions(t.Context(), "object", "account")
	if err != nil || len(versions) != 3 {
		t.Fatalf("rollback history was not append-only: versions=%#v err=%v", versions, err)
	}
	_, err = store.RollbackMetadataDefinition(t.Context(), "object", "account", metadatamodel.MetadataDefinitionRollbackRequest{TargetVersion: second.SchemaVersion, ExpectedSchemaHash: second.SchemaHash, ChangePlanID: "stale"}, auditmodel.AuditEvent{ID: "audit-stale", WorkspaceID: "default"})
	if conflict, ok := err.(*metadatamodel.MetadataDefinitionConflictError); !ok || conflict.CurrentHash != rolledBack.SchemaHash {
		t.Fatalf("expected stale rollback conflict, got %#v", err)
	}
}

func TestRelationFieldIndexesFollowMetadataContract(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	manifest := manifestmodel.ManifestSchema{TemplateID: "relation-index", Version: "1", Objects: []definitionmodel.ObjectSchema{
		{Key: "parent", Name: "Parent", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text"}}},
		{Key: "child", Name: "Child", Fields: []definitionmodel.FieldSchema{{Key: "parent_id", Name: "Parent", Type: "relation", Unique: true, Validation: definitionmodel.FieldValidation{Target: "parent"}, Config: map[string]any{"indexed": true, "cardinality": "one_to_one"}}}},
	}}
	if err := store.SyncManifestStorage(t.Context(), manifest); err != nil {
		t.Fatalf("sync one-to-one relation: %v", err)
	}
	uniqueName := store.metadataFieldIndexName("child", "parent_id", true)
	indexes, err := store.tableIndexes(t.Context(), "child")
	if err != nil || !indexes[uniqueName] {
		t.Fatalf("expected managed unique relation index, indexes=%#v err=%v", indexes, err)
	}
	manifest.Objects[1].Fields[0].Unique = false
	manifest.Objects[1].Fields[0].Config["cardinality"] = "many_to_one"
	if err := store.SyncManifestStorage(t.Context(), manifest); err != nil {
		t.Fatalf("sync many-to-one relation: %v", err)
	}
	indexes, err = store.tableIndexes(t.Context(), "child")
	normalName := store.metadataFieldIndexName("child", "parent_id", false)
	if err != nil || indexes[uniqueName] || !indexes[normalName] {
		t.Fatalf("expected unique index to become normal relation index, indexes=%#v err=%v", indexes, err)
	}
}

func TestManifestMetadataSyncAddsPluginDefinitionsWithoutOverwritingUserDefinitions(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()

	initial := manifestmodel.ManifestSchema{
		TemplateID: "plugin-app",
		Version:    "0.1.0",
		Objects: []definitionmodel.ObjectSchema{
			{
				Key:  "customer",
				Name: "Customer",
				Fields: []definitionmodel.FieldSchema{
					{Key: "name", Name: "Name", Type: "text"},
				},
			},
		},
	}
	if err := store.EnsureManifestMetadata(t.Context(), initial); err != nil {
		t.Fatalf("initial EnsureManifestMetadata: %v", err)
	}
	upgraded := manifestmodel.ManifestSchema{
		TemplateID: "plugin-app",
		Version:    "0.2.0",
		Objects: []definitionmodel.ObjectSchema{
			{
				Key:  "customer",
				Name: "Customer",
				Fields: []definitionmodel.FieldSchema{
					{Key: "name", Name: "Name", Type: "text"},
					{Key: "phone", Name: "Phone", Type: "phone"},
				},
			},
			{
				Key:  "opportunity",
				Name: "Opportunity",
				Fields: []definitionmodel.FieldSchema{
					{Key: "customer", Name: "Customer", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}},
				},
			},
		},
	}
	if err := store.EnsureManifestMetadata(t.Context(), upgraded); err != nil {
		t.Fatalf("upgraded EnsureManifestMetadata: %v", err)
	}
	manifest, err := store.LoadManifestMetadata(t.Context())
	if err != nil {
		t.Fatalf("LoadManifestMetadata after upgrade: %v", err)
	}
	if manifest.Version != "0.2.0" {
		t.Fatalf("expected synced template version 0.2.0, got %#v", manifest.Version)
	}
	assertGeneratedManifestField(t, manifest, "customer", "phone", "Phone")
	assertGeneratedManifestField(t, manifest, "opportunity", "customer", "Customer")
	if err := store.SyncManifestStorage(t.Context(), manifest); err != nil {
		t.Fatalf("SyncManifestStorage after metadata sync: %v", err)
	}
	customerColumns, err := store.tableColumns(t.Context(), "customer")
	if err != nil {
		t.Fatalf("customer columns: %v", err)
	}
	for _, column := range []string{"phone", "deleted", "ext_info", "create_by", "update_by"} {
		if !customerColumns[column] {
			t.Fatalf("expected synced customer.%s physical column, got %#v", column, customerColumns)
		}
	}

	customPayload, _ := json.Marshal(map[string]any{"key": "phone", "name": "VIP Phone", "type": "phone"})
	if _, err := store.UpsertMetadataDefinition(t.Context(), "field", "customer.phone", metadatamodel.MetadataDefinitionUpsertRequest{
		Payload:   json.RawMessage(customPayload),
		ObjectKey: "customer",
	}); err != nil {
		t.Fatalf("customize customer.phone: %v", err)
	}
	nextUpgrade := upgraded
	nextUpgrade.Version = "0.3.0"
	nextUpgrade.Objects[0].Fields[1].Name = "Generated Phone"
	if err := store.EnsureManifestMetadata(t.Context(), nextUpgrade); err != nil {
		t.Fatalf("next EnsureManifestMetadata: %v", err)
	}
	afterCustom, err := store.LoadManifestMetadata(t.Context())
	if err != nil {
		t.Fatalf("LoadManifestMetadata after custom sync: %v", err)
	}
	assertGeneratedManifestField(t, afterCustom, "customer", "phone", "VIP Phone")
}
