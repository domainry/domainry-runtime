package service

import (
	"context"
	"errors"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type reportSnapshotFaultStore struct {
	beginSnapshot reportmodel.ReportSnapshot
	beginExecute  bool
	beginErr      error
	completeErr   error
	latest        reportmodel.ReportSnapshot
	latestFound   bool
	latestErr     error
	failCodes     []string
}

func (s *reportSnapshotFaultStore) BeginReportSnapshot(context.Context, reportcontract.ReportSnapshotBeginRequest) (reportmodel.ReportSnapshot, bool, error) {
	return s.beginSnapshot, s.beginExecute, s.beginErr
}
func (s *reportSnapshotFaultStore) CompleteReportSnapshot(context.Context, reportcontract.ReportSnapshotCompleteRequest) error {
	return s.completeErr
}
func (s *reportSnapshotFaultStore) FailReportSnapshot(_ context.Context, _, _, code string) error {
	s.failCodes = append(s.failCodes, code)
	return nil
}
func (s *reportSnapshotFaultStore) LatestReportSnapshot(context.Context, string, string, string) (reportmodel.ReportSnapshot, bool, error) {
	return s.latest, s.latestFound, s.latestErr
}

type reportSnapshotFaultVersions struct {
	versions []reportmodel.ReportSnapshotSourceVersion
	errors   []error
	calls    int
}

func (r *reportSnapshotFaultVersions) ReadReportSnapshotSourceVersion(context.Context, reportcontract.ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error) {
	index := r.calls
	r.calls++
	if index < len(r.errors) && r.errors[index] != nil {
		return reportmodel.ReportSnapshotSourceVersion{}, r.errors[index]
	}
	if index < len(r.versions) {
		return r.versions[index], nil
	}
	return reportmodel.ReportSnapshotSourceVersion{}, nil
}

func reportSnapshotConditionFixture() (reportmodel.ReportSchema, reportmodel.ReportDatasetPlan, principalmodel.Principal) {
	report := reportmodel.ReportSchema{Key: "report", Materialization: &reportmodel.ReportMaterializationPolicy{MaximumLagSeconds: 60}, Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "entry", Alias: "entries"}}}
	plan, _ := reportcontract.BuildReportDatasetPlan(report)
	return report, plan, principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace", UserID: "user"}}
}

func TestRefreshSnapshotCoversLookupMaterializationIdempotencyDependencyPlanScopeAndBeginEdges(t *testing.T) {
	report, plan, principal := reportSnapshotConditionFixture()
	_ = plan
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	base := ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{report}
	}, Access: reportDatasetAccess{objects: map[string]definitionmodel.ObjectSchema{"entry": {Key: "entry"}}}, Records: reportDatasetRecords{}, Clock: func() time.Time { return now }}

	if _, err := NewReportDomainService(base).RefreshSnapshot(t.Context(), "missing", "key", principal); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("missing err=%v", err)
	}
	withoutMaterialization := report
	withoutMaterialization.Materialization = nil
	dependencies := base
	dependencies.Reports = func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{withoutMaterialization}
	}
	if _, err := NewReportDomainService(dependencies).RefreshSnapshot(t.Context(), report.Key, "key", principal); apperror.CodeOf(err) != "backend.report.materialization_not_enabled" {
		t.Fatalf("materialization err=%v", err)
	}
	if _, err := NewReportDomainService(base).RefreshSnapshot(t.Context(), report.Key, " ", principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
		t.Fatalf("key err=%v", err)
	}
	dependencies = base
	dependencies.SnapshotSources = &reportSnapshotFaultVersions{}
	if _, err := NewReportDomainService(dependencies).RefreshSnapshot(t.Context(), report.Key, "key", principal); apperror.CodeOf(err) != "backend.report.snapshot_unavailable" {
		t.Fatalf("nil store err=%v", err)
	}
	dependencies = base
	dependencies.Snapshots = &reportSnapshotFaultStore{}
	if _, err := NewReportDomainService(dependencies).RefreshSnapshot(t.Context(), report.Key, "key", principal); apperror.CodeOf(err) != "backend.report.snapshot_unavailable" {
		t.Fatalf("nil sources err=%v", err)
	}

	invalidPlan := report
	invalidPlan.Dataset.Source = reportmodel.ReportDatasetSource{}
	dependencies = base
	dependencies.Reports = func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{invalidPlan}
	}
	dependencies.Snapshots = &reportSnapshotFaultStore{}
	dependencies.SnapshotSources = &reportSnapshotFaultVersions{}
	if _, err := NewReportDomainService(dependencies).RefreshSnapshot(t.Context(), report.Key, "key", principal); apperror.CodeOf(err) != "backend.report.plan_invalid" {
		t.Fatalf("plan err=%v", err)
	}

	badScope := principal
	badScope.BusinessClaims = map[string]profilebindingmodel.ClaimValue{"bad": {Type: "json", Value: make(chan int)}}
	dependencies = base
	dependencies.Snapshots = &reportSnapshotFaultStore{}
	dependencies.SnapshotSources = &reportSnapshotFaultVersions{}
	if _, err := NewReportDomainService(dependencies).RefreshSnapshot(t.Context(), report.Key, "key", badScope); apperror.CodeOf(err) != "backend.report.snapshot_scope_invalid" {
		t.Fatalf("scope err=%v", err)
	}

	want := errors.New("begin failed")
	store := &reportSnapshotFaultStore{beginErr: want}
	dependencies.Snapshots = store
	if _, err := NewReportDomainService(dependencies).RefreshSnapshot(t.Context(), report.Key, "key", principal); !errors.Is(err, want) || apperror.CodeOf(err) != "backend.report.snapshot_begin_failed" {
		t.Fatalf("begin err=%v", err)
	}
	store.beginErr = nil
	store.beginSnapshot = reportmodel.ReportSnapshot{ID: "existing", Status: "succeeded"}
	store.beginExecute = false
	snapshot, err := NewReportDomainService(dependencies).RefreshSnapshot(t.Context(), report.Key, "key", principal)
	if err != nil || snapshot.ID != "existing" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}

func TestRefreshSnapshotCoversSourceReadExecutionRetryAndFenceFailures(t *testing.T) {
	report, _, principal := reportSnapshotConditionFixture()
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	newDependencies := func(store *reportSnapshotFaultStore, versions *reportSnapshotFaultVersions) ReportDependencies {
		return ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		}, Access: reportDatasetAccess{objects: map[string]definitionmodel.ObjectSchema{"entry": {Key: "entry"}}}, Records: reportDatasetRecords{}, Snapshots: store, SnapshotSources: versions, Clock: func() time.Time { return now }}
	}
	want := errors.New("failure")
	store := &reportSnapshotFaultStore{beginSnapshot: reportmodel.ReportSnapshot{ID: "snapshot", Status: "refreshing"}, beginExecute: true}

	dependencies := newDependencies(store, &reportSnapshotFaultVersions{})
	dependencies.Access = reportFaultAccess{objectErr: want}
	if _, err := NewReportDomainService(dependencies).RefreshSnapshot(t.Context(), report.Key, "key", principal); !errors.Is(err, want) || len(store.failCodes) != 1 {
		t.Fatalf("source err=%v fail=%v", err, store.failCodes)
	}
	store.failCodes = nil
	versions := &reportSnapshotFaultVersions{errors: []error{want}}
	if _, err := NewReportDomainService(newDependencies(store, versions)).RefreshSnapshot(t.Context(), report.Key, "key", principal); apperror.CodeOf(err) != "backend.report.snapshot_refresh_failed" || len(store.failCodes) != 1 {
		t.Fatalf("before version err=%v fail=%v", err, store.failCodes)
	}
	store.failCodes = nil
	dependencies = newDependencies(store, &reportSnapshotFaultVersions{versions: []reportmodel.ReportSnapshotSourceVersion{{}}})
	dependencies.Records = reportFaultRecords{err: want}
	if _, err := NewReportDomainService(dependencies).RefreshSnapshot(t.Context(), report.Key, "key", principal); apperror.CodeOf(err) != "backend.report.snapshot_refresh_failed" {
		t.Fatalf("execute err=%v", err)
	}
	store.failCodes = nil
	versions = &reportSnapshotFaultVersions{versions: []reportmodel.ReportSnapshotSourceVersion{{}}, errors: []error{nil, want}}
	if _, err := NewReportDomainService(newDependencies(store, versions)).RefreshSnapshot(t.Context(), report.Key, "key", principal); apperror.CodeOf(err) != "backend.report.snapshot_refresh_failed" {
		t.Fatalf("after version err=%v", err)
	}

	stableVersion := reportmodel.ReportSnapshotSourceVersion{Watermark: "stable"}
	store.completeErr = want
	versions = &reportSnapshotFaultVersions{versions: []reportmodel.ReportSnapshotSourceVersion{stableVersion, stableVersion}}
	if _, err := NewReportDomainService(newDependencies(store, versions)).RefreshSnapshot(t.Context(), report.Key, "key", principal); apperror.CodeOf(err) != "backend.report.snapshot_fenced" {
		t.Fatalf("complete err=%v", err)
	}
}

func TestReadReportSnapshotCoversMaterializationStoreScopeReadParseAndFutureClockEdges(t *testing.T) {
	report, _, principal := reportSnapshotConditionFixture()
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	service := NewReportDomainService(ReportDependencies{Clock: func() time.Time { return now }})
	without := report
	without.Materialization = nil
	if _, err := service.readReportSnapshot(t.Context(), without, principal); apperror.CodeOf(err) != "backend.report.materialization_not_enabled" {
		t.Fatalf("materialization err=%v", err)
	}
	if _, err := service.readReportSnapshot(t.Context(), report, principal); apperror.CodeOf(err) != "backend.report.snapshot_unavailable" {
		t.Fatalf("store err=%v", err)
	}
	badScope := principal
	badScope.BusinessClaims = map[string]profilebindingmodel.ClaimValue{"bad": {Value: make(chan int)}}
	store := &reportSnapshotFaultStore{}
	service = NewReportDomainService(ReportDependencies{Snapshots: store, Clock: func() time.Time { return now }})
	if _, err := service.readReportSnapshot(t.Context(), report, badScope); apperror.CodeOf(err) != "backend.report.snapshot_scope_invalid" {
		t.Fatalf("scope err=%v", err)
	}
	want := errors.New("read failed")
	store.latestErr = want
	if _, err := service.readReportSnapshot(t.Context(), report, principal); !errors.Is(err, want) || apperror.CodeOf(err) != "backend.report.snapshot_read_failed" {
		t.Fatalf("read err=%v", err)
	}
	store.latestErr = nil
	if _, err := service.readReportSnapshot(t.Context(), report, principal); apperror.CodeOf(err) != "backend.report.snapshot_not_found" {
		t.Fatalf("not found err=%v", err)
	}
	store.latestFound = true
	store.latest = reportmodel.ReportSnapshot{ID: "snapshot", RefreshedAt: "bad"}
	if _, err := service.readReportSnapshot(t.Context(), report, principal); apperror.CodeOf(err) != "backend.report.snapshot_invalid" {
		t.Fatalf("parse err=%v", err)
	}
	store.latest.RefreshedAt = now.Add(time.Minute).Format(time.RFC3339Nano)
	summary, err := service.readReportSnapshot(t.Context(), report, principal)
	if err != nil || summary.Snapshot == nil || summary.Snapshot.LagSeconds != 0 || summary.Snapshot.Stale {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
}

func TestReportSnapshotScopeHashRejectsUnsupportedClaimAndVersionEqualityCoversBothOutcomes(t *testing.T) {
	if _, err := reportSnapshotAccessScopeHash(principalmodel.Principal{BusinessClaims: map[string]profilebindingmodel.ClaimValue{"bad": {Value: make(chan int)}}}); err == nil {
		t.Fatal("unsupported claim encoded")
	}
	version := reportmodel.ReportSnapshotSourceVersion{Watermark: "one"}
	if !reportSnapshotVersionsEqual(version, version) || reportSnapshotVersionsEqual(version, reportmodel.ReportSnapshotSourceVersion{Watermark: "two"}) {
		t.Fatal("version equality mismatch")
	}
}

func TestCommitSnapshotRefreshUnknownTerminalStatusIsNoop(t *testing.T) {
	service := NewReportDomainService(ReportDependencies{Snapshots: &reportSnapshotFaultStore{}})
	if err := service.CommitSnapshotRefresh(t.Context(), ReportSnapshotRefresh{TerminalStatus: "unknown"}); err != nil {
		t.Fatalf("unknown terminal status=%v", err)
	}
}
