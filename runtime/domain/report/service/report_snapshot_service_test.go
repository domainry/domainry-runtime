package service

import (
	"context"
	"errors"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestReportAccessScopeHashCanonicalizesSetOrderingWithoutWeakeningFacts(t *testing.T) {
	left := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default", UserID: "demo-user", AuthorizationRevision: "revision-1",
		ReportingUserIDs: []string{"user-b", "user-a"}, OrganizationScopes: identitysdk.OrganizationScopes{StoreIDs: []string{"store-b", "store-a"}}},

		BusinessProfiles: []profilebindingmodel.Reference{
			{BindingKey: "member-b", ObjectKey: "member", RecordID: "two", SurfaceKeys: []string{"portal-b", "portal-a"}},
			{BindingKey: "member-a", ObjectKey: "member", RecordID: "one", SurfaceKeys: []string{"portal-a"}},
		},
	}, accessfixture.Bundle{Key: "group_admin", Permissions: []string{"sales.export", "sales.read"}},
	)
	right := left
	right.ReportingUserIDs = []string{"user-a", "user-b", "user-a"}
	right.OrganizationScopes.StoreIDs = []string{"store-a", "store-b"}
	right.BusinessProfiles = []profilebindingmodel.Reference{left.BusinessProfiles[1], left.BusinessProfiles[0]}
	right.BusinessProfiles[1].SurfaceKeys = []string{"portal-a", "portal-b"}
	leftHash, err := ReportAccessScopeHash(left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := ReportAccessScopeHash(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("set-order-only principal drift changed hash: left=%s right=%s", leftHash, rightHash)
	}
	right.AuthorizationRevision = "revision-2"
	changedHash, err := ReportAccessScopeHash(right)
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == leftHash {
		t.Fatal("real authorization revision change did not change report access scope hash")
	}
}

type reportSnapshotMemoryStore struct {
	byKey map[string]reportmodel.ReportSnapshot
	begin int
	fails int
}

func (s *reportSnapshotMemoryStore) BeginReportSnapshot(_ context.Context, request reportcontract.ReportSnapshotBeginRequest) (reportmodel.ReportSnapshot, bool, error) {
	s.begin++
	if s.byKey == nil {
		s.byKey = map[string]reportmodel.ReportSnapshot{}
	}
	key := request.WorkspaceID + ":" + request.ReportKey + ":" + request.AccessScopeHash + ":" + request.IdempotencyKey
	if snapshot, ok := s.byKey[key]; ok {
		return snapshot, snapshot.Status != "succeeded", nil
	}
	snapshot := reportmodel.ReportSnapshot{ID: "snapshot-1", WorkspaceID: request.WorkspaceID, ReportKey: request.ReportKey, AccessScopeHash: request.AccessScopeHash, IdempotencyKey: request.IdempotencyKey, Status: "refreshing", StartedAt: request.StartedAt}
	s.byKey[key] = snapshot
	return snapshot, true, nil
}

func (s *reportSnapshotMemoryStore) CompleteReportSnapshot(_ context.Context, request reportcontract.ReportSnapshotCompleteRequest) error {
	for key, current := range s.byKey {
		if current.ID == request.Snapshot.ID {
			if current.Status != request.ExpectedStatus {
				return errors.New("snapshot fence lost")
			}
			s.byKey[key] = request.Snapshot
			return nil
		}
	}
	return errors.New("snapshot missing")
}

func (s *reportSnapshotMemoryStore) FailReportSnapshot(_ context.Context, id, expectedStatus, code string) error {
	s.fails++
	for key, current := range s.byKey {
		if current.ID == id && current.Status == expectedStatus {
			current.Status, current.ErrorCode = "failed", code
			s.byKey[key] = current
			return nil
		}
	}
	return errors.New("snapshot fence lost")
}

func (s *reportSnapshotMemoryStore) LatestReportSnapshot(_ context.Context, workspaceID, reportKey, scopeHash string) (reportmodel.ReportSnapshot, bool, error) {
	var latest reportmodel.ReportSnapshot
	for _, snapshot := range s.byKey {
		if snapshot.WorkspaceID == workspaceID && snapshot.ReportKey == reportKey && snapshot.AccessScopeHash == scopeHash && snapshot.Status == "succeeded" && snapshot.RefreshedAt > latest.RefreshedAt {
			latest = snapshot
		}
	}
	return latest, latest.ID != "", nil
}

type reportSnapshotVersionReader struct {
	versions []reportmodel.ReportSnapshotSourceVersion
	calls    int
}

func (r *reportSnapshotVersionReader) ReadReportSnapshotSourceVersion(context.Context, reportcontract.ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error) {
	index := r.calls
	r.calls++
	if index >= len(r.versions) {
		index = len(r.versions) - 1
	}
	return r.versions[index], nil
}

func TestReportSnapshotRefreshIsScopedIdempotentAndExposesFreshness(t *testing.T) {
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	store := &reportSnapshotMemoryStore{}
	version := reportmodel.ReportSnapshotSourceVersion{Watermark: "2026-07-21T09:59:00Z", SourceVersions: map[string]string{"entries": "2:source-hash"}}
	versions := &reportSnapshotVersionReader{versions: []reportmodel.ReportSnapshotSourceVersion{version, version}}
	report := reportmodel.ReportSchema{Key: "totals", Materialization: &reportmodel.ReportMaterializationPolicy{MaximumLagSeconds: 60}, Dataset: reportmodel.ReportDatasetSchema{
		Source: reportmodel.ReportDatasetSource{ObjectKey: "entry", Alias: "entries"}, Measures: []reportmodel.ReportDatasetMeasure{{Key: "amount", Operation: "sum", Field: reportField("entries", "amount")}},
	}}
	service := NewReportDomainService(ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: reportDatasetAccess{objects: map[string]definitionmodel.ObjectSchema{"entry": {Key: "entry", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency"}}}}},
		Records: reportDatasetRecords{byObject: map[string][]recordmodel.Record{
			"entry": {
				{ID: "e-1", Data: map[string]any{"amount": "10.00"}},
				{ID: "e-2", Data: map[string]any{"amount": "20.00"}},
			},
		}},
		Snapshots: store, SnapshotSources: versions, Clock: func() time.Time { return now },
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "analyst", AuthorizationRevision: "7"}}, accessfixture.Bundle{Key: "analyst"})
	snapshot, err := service.RefreshSnapshot(t.Context(), "totals", "window-1", principal)
	if err != nil || snapshot.Status != "succeeded" || snapshot.Summary.Rows[0].Measures["amount"] != "30.00" || versions.calls != 2 {
		t.Fatalf("snapshot=%#v calls=%d err=%v", snapshot, versions.calls, err)
	}
	if replay, replayErr := service.RefreshSnapshot(t.Context(), "totals", "window-1", principal); replayErr != nil || replay.ID != snapshot.ID || versions.calls != 2 {
		t.Fatalf("replay=%#v calls=%d err=%v", replay, versions.calls, replayErr)
	}
	now = now.Add(61 * time.Second)
	summary, err := service.SummaryMode(t.Context(), "totals", "snapshot", principal)
	if err != nil || summary.ExecutionMode != "snapshot" || summary.Snapshot == nil || summary.Snapshot.LagSeconds != 61 || !summary.Snapshot.Stale || summary.Snapshot.SourceVersions["entries"] != "2:source-hash" {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	other := principal
	other.UserID = "other"
	if _, err := service.SummaryMode(t.Context(), "totals", "snapshot", other); apperror.CodeOf(err) != "backend.report.snapshot_not_found" {
		t.Fatalf("cross-scope snapshot err=%v", err)
	}
}

func TestReportSnapshotRefreshFailsAfterBoundedSourceVersionDrift(t *testing.T) {
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	store := &reportSnapshotMemoryStore{}
	versions := &reportSnapshotVersionReader{versions: []reportmodel.ReportSnapshotSourceVersion{
		{SourceVersions: map[string]string{"events": "1"}}, {SourceVersions: map[string]string{"events": "2"}},
		{SourceVersions: map[string]string{"events": "3"}}, {SourceVersions: map[string]string{"events": "4"}},
	}}
	report := reportmodel.ReportSchema{Key: "events", Materialization: &reportmodel.ReportMaterializationPolicy{MaximumLagSeconds: 60, ConsistencyRetries: 2}, Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"}}}
	service := NewReportDomainService(ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: reportDatasetAccess{objects: map[string]definitionmodel.ObjectSchema{"event": {Key: "event"}}}, Records: reportDatasetRecords{},
		Snapshots: store, SnapshotSources: versions, Clock: func() time.Time { return now },
	})
	_, err := service.RefreshSnapshot(t.Context(), "events", "window-1", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "analyst"}})
	if apperror.CodeOf(err) != "backend.report.snapshot_source_changed" || store.fails != 1 || versions.calls != 4 {
		t.Fatalf("err=%v fails=%d calls=%d", err, store.fails, versions.calls)
	}
}
