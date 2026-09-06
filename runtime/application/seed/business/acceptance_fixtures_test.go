package businessseed

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	businessseedcontract "github.com/domainry/domainry-runtime/runtime/domain/businessseed/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type acceptanceFixtureStoreProbe struct {
	records map[string]recordmodel.Record
	commits []transactionmodel.RecordMutationCommit
}

func (s *acceptanceFixtureStoreProbe) GetRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	record, found := s.records[recordID]
	return record, found, nil
}

func (s *acceptanceFixtureStoreProbe) CommitRecordMutation(_ context.Context, _ string, commit transactionmodel.RecordMutationCommit) error {
	s.commits = append(s.commits, commit)
	s.records[commit.Record.ID] = commit.Record
	return nil
}

func TestSyncRuntimeAcceptanceFixturesUsesInjectedClockAndAtomicAudit(t *testing.T) {
	now := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	store := &acceptanceFixtureStoreProbe{records: map[string]recordmodel.Record{}}
	raw := acceptanceFixtureJSON(t, validAcceptanceFixtureEnvelope())
	if err := SyncRuntimeAcceptanceFixtures(t.Context(), store, acceptanceFixtureManifest(), "workspace-1", "workspace-primary", acceptanceIdentityReferences(), raw, now); err != nil {
		t.Fatal(err)
	}
	if len(store.commits) != 1 {
		t.Fatalf("commits=%d", len(store.commits))
	}
	commit := store.commits[0]
	wantChangedAt := now.Add(-192 * time.Hour).Format(time.RFC3339Nano)
	if commit.Record.Data["last_status_changed_at"] != wantChangedAt {
		t.Fatalf("relative datetime=%v want=%s", commit.Record.Data["last_status_changed_at"], wantChangedAt)
	}
	if commit.Record.WorkspaceID != "workspace-1" || commit.Record.OwnerUserID != "rep-1" || commit.Record.OwnerOrgID != "east" {
		t.Fatalf("record boundary=%+v", commit.Record)
	}
	if commit.Audit == nil || commit.Audit.Event != acceptanceFixtureAuditEvent || commit.Audit.WorkspaceID != "workspace-1" || commit.Audit.RecordID != commit.Record.ID {
		t.Fatalf("atomic audit=%+v", commit.Audit)
	}
	if commit.Audit.CreatedAt != now.Format(time.RFC3339Nano) || commit.Audit.Metadata["fixture_key"] != "aged_lead" {
		t.Fatalf("audit clock/metadata=%+v", commit.Audit)
	}

	stored := store.records[commit.Record.ID]
	stored.Data["status"] = "mutated_after_start"
	store.records[commit.Record.ID] = stored
	if err := SyncRuntimeAcceptanceFixtures(t.Context(), store, acceptanceFixtureManifest(), "workspace-1", "workspace-primary", acceptanceIdentityReferences(), raw, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(store.commits) != 1 || store.records[commit.Record.ID].Data["status"] != "mutated_after_start" {
		t.Fatalf("restart replayed fixture: commits=%d record=%+v", len(store.commits), store.records[commit.Record.ID])
	}
}

func TestSyncRuntimeAcceptanceFixturesRejectsBoundaryViolations(t *testing.T) {
	now := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		mutate    func(*acceptanceFixtureEnvelope)
		manifest  manifestmodel.ManifestSchema
		workspace string
	}{
		{name: "Workspace mismatch", mutate: func(e *acceptanceFixtureEnvelope) { e.WorkspaceCode = "other" }, manifest: acceptanceFixtureManifest(), workspace: "workspace-primary"},
		{name: "unknown actor", mutate: func(e *acceptanceFixtureEnvelope) { e.BusinessRecords[0].OwnerActorID = "missing" }, manifest: acceptanceFixtureManifest(), workspace: "workspace-primary"},
		{name: "actor absent from committed graph", mutate: func(e *acceptanceFixtureEnvelope) {
			e.Actors = append(e.Actors, acceptanceFixtureActor{ID: "declared-only", OrganizationID: "east"})
			e.BusinessRecords[0].OwnerActorID = "declared-only"
		}, manifest: acceptanceFixtureManifest(), workspace: "workspace-primary"},
		{name: "actor organization mismatch", mutate: func(e *acceptanceFixtureEnvelope) { e.BusinessRecords[0].OwnerOrganizationID = "west" }, manifest: acceptanceFixtureManifest(), workspace: "workspace-primary"},
		{name: "organization absent from committed graph", mutate: func(e *acceptanceFixtureEnvelope) {
			e.Actors[0].OrganizationID = "west"
			e.BusinessRecords[0].OwnerOrganizationID = "west"
		}, manifest: acceptanceFixtureManifest(), workspace: "workspace-primary"},
		{name: "future relative time", mutate: func(e *acceptanceFixtureEnvelope) {
			e.BusinessRecords[0].RelativeTimes["last_status_changed_at"] = "1h"
		}, manifest: acceptanceFixtureManifest(), workspace: "workspace-primary"},
		{name: "unbounded relative time", mutate: func(e *acceptanceFixtureEnvelope) {
			e.BusinessRecords[0].RelativeTimes["last_status_changed_at"] = "-9000h"
		}, manifest: acceptanceFixtureManifest(), workspace: "workspace-primary"},
		{name: "non temporal field", mutate: func(e *acceptanceFixtureEnvelope) {
			e.BusinessRecords[0].RelativeTimes = map[string]string{"status": "-1h"}
		}, manifest: acceptanceFixtureManifest(), workspace: "workspace-primary"},
		{name: "direct crud object", mutate: func(*acceptanceFixtureEnvelope) {}, manifest: manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "lead", Fields: acceptanceFixtureManifest().Objects[0].Fields}}}, workspace: "workspace-primary"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := validAcceptanceFixtureEnvelope()
			test.mutate(&envelope)
			store := &acceptanceFixtureStoreProbe{records: map[string]recordmodel.Record{}}
			if err := SyncRuntimeAcceptanceFixtures(t.Context(), store, test.manifest, "workspace-1", test.workspace, acceptanceIdentityReferences(), acceptanceFixtureJSON(t, envelope), now); err == nil {
				t.Fatal("expected rejection")
			}
			if len(store.commits) != 0 {
				t.Fatalf("invalid fixture committed %d records", len(store.commits))
			}
		})
	}
}

func TestSyncRuntimeAcceptanceFixturesRejectsExistingRecordCollision(t *testing.T) {
	store := &acceptanceFixtureStoreProbe{records: map[string]recordmodel.Record{
		"acceptance_aged_lead": {ID: "acceptance_aged_lead", ExtInfo: map[string]any{"source_kind": "business"}},
	}}
	err := SyncRuntimeAcceptanceFixtures(t.Context(), store, acceptanceFixtureManifest(), "workspace-1", "workspace-primary", acceptanceIdentityReferences(), acceptanceFixtureJSON(t, validAcceptanceFixtureEnvelope()), time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC))
	if err == nil || len(store.commits) != 0 {
		t.Fatalf("collision error=%v commits=%d", err, len(store.commits))
	}
}

func acceptanceIdentityReferences() []AcceptanceFixtureIdentityReference {
	return []AcceptanceFixtureIdentityReference{{ObjectKey: "identity_user", RecordID: "rep-1"}, {ObjectKey: "identity_organization_unit", RecordID: "east"}}
}

func validAcceptanceFixtureEnvelope() acceptanceFixtureEnvelope {
	return acceptanceFixtureEnvelope{
		ContractVersion: businessseedcontract.RuntimeAcceptanceFixtureContractVersion, WorkspaceCode: "workspace-primary",
		Organizations: []acceptanceFixtureOrganization{{ID: "east"}, {ID: "west"}},
		Actors:        []acceptanceFixtureActor{{ID: "rep-1", OrganizationID: "east"}},
		BusinessRecords: []acceptanceFixtureBusinessRecord{{
			FixtureKey: "aged_lead", ObjectKey: "lead", RecordID: "acceptance_aged_lead",
			Data: map[string]any{"name": "Aged lead", "status": "open"}, OwnerActorID: "rep-1", OwnerOrganizationID: "east",
			RelativeTimes: map[string]string{"last_status_changed_at": "-192h"},
		}},
	}
}

func TestAcceptanceFixtureV2RoundTripRejectsLegacyTenantCode(t *testing.T) {
	envelope := validAcceptanceFixtureEnvelope()
	raw := acceptanceFixtureJSON(t, envelope)
	var roundTrip acceptanceFixtureEnvelope
	if err := json.Unmarshal([]byte(raw), &roundTrip); err != nil || roundTrip.ContractVersion != businessseedcontract.RuntimeAcceptanceFixtureContractVersion || roundTrip.WorkspaceCode != envelope.WorkspaceCode {
		t.Fatalf("V2 round trip=%#v err=%v", roundTrip, err)
	}
	legacy := strings.Replace(raw, `"workspace_code":"workspace-primary"`, `"tenant_code":"workspace-primary"`, 1)
	store := &acceptanceFixtureStoreProbe{records: map[string]recordmodel.Record{}}
	err := SyncRuntimeAcceptanceFixtures(t.Context(), store, acceptanceFixtureManifest(), "workspace-1", "workspace-primary", acceptanceIdentityReferences(), legacy, time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "unknown field") || len(store.commits) != 0 {
		t.Fatalf("legacy tenant_code error=%v commits=%d", err, len(store.commits))
	}
}

func acceptanceFixtureManifest() manifestmodel.ManifestSchema {
	return manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "lead", Config: map[string]any{"write_policy": "action_only"},
		Fields: []definitionmodel.FieldSchema{
			{Key: "name", Type: "text", Required: true},
			{Key: "status", Type: "text", Required: true},
			{Key: "last_status_changed_at", Type: "datetime", Required: true},
		},
	}}}
}

func acceptanceFixtureJSON(t *testing.T, envelope acceptanceFixtureEnvelope) string {
	t.Helper()
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
