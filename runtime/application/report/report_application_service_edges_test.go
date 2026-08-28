package report

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type reportEmptyAccess struct{}

func (reportEmptyAccess) ReportObjectForAction(context.Context, principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
	return definitionmodel.ObjectSchema{Key: "empty"}, nil
}
func (reportEmptyAccess) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}
func (reportEmptyAccess) CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}

type reportEmptyRecords struct{}

func (reportEmptyRecords) ListReportRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{}, nil
}

type reportSnapshotStoreStub struct {
	completed   reportmodel.ReportSnapshot
	latest      reportmodel.ReportSnapshot
	found       bool
	latestErr   error
	failed      string
	completeErr error
	failErr     error
}

func (s *reportSnapshotStoreStub) BeginReportSnapshot(_ context.Context, request reportcontract.ReportSnapshotBeginRequest) (reportmodel.ReportSnapshot, bool, error) {
	return reportmodel.ReportSnapshot{ID: "snapshot-1", WorkspaceID: request.WorkspaceID, ReportKey: request.ReportKey, IdempotencyKey: request.IdempotencyKey, Status: "refreshing", StartedAt: request.StartedAt}, true, nil
}
func (s *reportSnapshotStoreStub) CompleteReportSnapshot(_ context.Context, request reportcontract.ReportSnapshotCompleteRequest) error {
	s.completed = request.Snapshot
	return s.completeErr
}
func (s *reportSnapshotStoreStub) FailReportSnapshot(_ context.Context, _, _, code string) error {
	s.failed = code
	return s.failErr
}
func (s *reportSnapshotStoreStub) LatestReportSnapshot(context.Context, string, string, string) (reportmodel.ReportSnapshot, bool, error) {
	return s.latest, s.found, s.latestErr
}

type reportNotificationCommitterStub struct {
	store  *reportSnapshotStoreStub
	intent notificationmodel.NotificationIntent
	event  notificationmodel.NotificationEvent
	err    error
}

func (s *reportNotificationCommitterStub) CompleteReportSnapshotWithNotification(ctx context.Context, request reportcontract.ReportSnapshotCompleteRequest, event notificationmodel.NotificationEvent) error {
	s.event = event
	if s.err != nil {
		return s.err
	}
	return s.store.CompleteReportSnapshot(ctx, request)
}

func (s *reportNotificationCommitterStub) FailReportSnapshotWithNotification(ctx context.Context, id, expectedStatus, code string, event notificationmodel.NotificationEvent) error {
	s.event = event
	if s.err != nil {
		return s.err
	}
	return s.store.FailReportSnapshot(ctx, id, expectedStatus, code)
}

type reportSnapshotSourceStub struct{ err error }

func (s reportSnapshotSourceStub) ReadReportSnapshotSourceVersion(context.Context, reportcontract.ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error) {
	return reportmodel.ReportSnapshotSourceVersion{Watermark: "stable", SourceVersions: map[string]string{"empty": "v1"}}, s.err
}

func TestReportApplicationSummaryRejectsMissingWorkspaceBeforeDomain(t *testing.T) {
	service := reportApplicationFixture(&reportRecordExporterStub{}, &reportAuditAppenderStub{})

	_, err := service.Summary(t.Context(), "revenue", principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator-1"}})
	if err == nil || apperror.KindOf(err) != apperror.KindForbidden || apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("missing workspace error = %v", err)
	}
}

func TestReportApplicationSummaryDelegatesWithAuthorizedWorkspace(t *testing.T) {
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{{Key: "empty", Name: "Empty report", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "empty", Alias: "empty"}}}}
		},
		Access: reportEmptyAccess{}, Records: reportEmptyRecords{},
	})
	service := NewReportApplicationService(ReportApplicationDependencies{Domain: domain})

	summary, err := service.Summary(t.Context(), " empty ", reportPrincipal())
	if err != nil || summary.Key != "empty" || summary.Name != "Empty report" || summary.RowCount != 1 || len(summary.Rows) != 1 {
		t.Fatalf("summary=%#v error=%v", summary, err)
	}
}

func TestReportApplicationExportRequiresAuditDependency(t *testing.T) {
	records := &reportRecordExporterStub{}
	_, _, err := reportApplicationFixture(records, nil).ExportObject(t.Context(), "revenue", "customer", reportPrincipal())
	if err == nil || apperror.KindOf(err) != apperror.KindInternal || apperror.CodeOf(err) != "backend.internal_error" || records.calls != 1 {
		t.Fatalf("missing audit error=%v record_calls=%d", err, records.calls)
	}
}

func TestReportApplicationExportRejectsUnknownReportBeforeRecordPort(t *testing.T) {
	records := &reportRecordExporterStub{}
	audit := &reportAuditAppenderStub{}
	_, _, err := reportApplicationFixture(records, audit).ExportObject(t.Context(), "missing", "customer", reportPrincipal())
	if err == nil || apperror.KindOf(err) != apperror.KindNotFound || apperror.CodeOf(err) != "backend.report.not_found" || records.calls != 0 || audit.calls != 0 {
		t.Fatalf("unknown report error=%v record_calls=%d audit_calls=%d", err, records.calls, audit.calls)
	}
}

func TestReportApplicationRefreshDelegatesAfterWorkspaceAuthorization(t *testing.T) {
	service := reportApplicationFixture(&reportRecordExporterStub{}, &reportAuditAppenderStub{})
	if _, err := service.RefreshSnapshot(t.Context(), "missing", "refresh-1", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace refresh error=%v", err)
	}
	if _, err := service.RefreshSnapshot(t.Context(), "missing", "refresh-1", reportPrincipal()); err == nil {
		t.Fatal("domain refresh error was not propagated")
	}
}

func TestReportSnapshotTerminalNotificationUsesTypedSafeIntent(t *testing.T) {
	intents := []notificationmodel.NotificationIntent{}
	service := NewReportApplicationService(ReportApplicationDependencies{NotificationCompiler: func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		intents = append(intents, intent)
		return notificationmodel.NotificationEvent{EventType: intent.EventType}, nil
	}})
	completed := reportservice.ReportSnapshotRefresh{Snapshot: reportmodel.ReportSnapshot{ID: "snapshot-1", Status: "succeeded"}, TerminalStatus: "succeeded"}
	if _, notify, err := service.snapshotTerminalNotification(" revenue ", "refresh-1", completed, reportPrincipal()); err != nil || !notify {
		t.Fatalf("notify=%v err=%v", notify, err)
	}
	if len(intents) != 1 || intents[0].EventType != "report.snapshot.completed" || intents[0].SubjectType != "report" || intents[0].SubjectID != "revenue" || intents[0].RecipientUserIDs[0] != "operator-1" {
		t.Fatalf("intent=%+v", intents)
	}
	if len(intents[0].Variables) != 3 || intents[0].Variables["report_key"] != "revenue" || intents[0].Variables["status"] != "completed" {
		t.Fatalf("unsafe or incomplete variables=%+v", intents[0].Variables)
	}
	failed := reportservice.ReportSnapshotRefresh{Snapshot: reportmodel.ReportSnapshot{Status: "failed"}, TerminalStatus: "failed", ErrorCode: "backend.report.snapshot_refresh_failed"}
	if _, notify, err := service.snapshotTerminalNotification("revenue", "refresh-2", failed, principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}); err != nil || notify {
		t.Fatalf("recipient-free notify=%v err=%v", notify, err)
	}
	if len(intents) != 1 {
		t.Fatalf("notification emitted without recipient: %+v", intents)
	}
	service.compileNotification = func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, errors.New("notification unavailable")
	}
	if _, _, err := service.snapshotTerminalNotification("revenue", "refresh-3", failed, reportPrincipal()); err == nil {
		t.Fatal("expected compiler failure")
	}
}

func TestReportRefreshPublishesIdempotentTerminalResult(t *testing.T) {
	for _, test := range []struct {
		name      string
		sourceErr error
		wantEvent string
		wantError bool
	}{
		{name: "completed", wantEvent: "report.snapshot.completed"},
		{name: "failed", sourceErr: errors.New("source unavailable"), wantEvent: "report.snapshot.failed", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &reportSnapshotStoreStub{}
			domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
				Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
					return []reportmodel.ReportSchema{{Key: "empty", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "empty", Alias: "empty"}, Measures: []reportmodel.ReportDatasetMeasure{{Key: "count", Operation: "count"}}}, Materialization: &reportmodel.ReportMaterializationPolicy{MaximumLagSeconds: 60, ConsistencyRetries: 1}}}
				},
				Access: reportEmptyAccess{}, Records: reportEmptyRecords{}, Snapshots: store, SnapshotSources: reportSnapshotSourceStub{err: test.sourceErr},
			})
			committer := &reportNotificationCommitterStub{store: store}
			service := NewReportApplicationService(ReportApplicationDependencies{Domain: domain, NotificationCompiler: func(value notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
				committer.intent = value
				return notificationmodel.NotificationEvent{EventType: value.EventType}, nil
			}, NotificationCommitter: committer})
			snapshot, err := service.RefreshSnapshot(t.Context(), "empty", "refresh-1", reportPrincipal())
			if (err != nil) != test.wantError || committer.intent.EventType != test.wantEvent || committer.intent.SourceEventID != "report-snapshot:empty:refresh-1:"+committer.intent.Variables["status"].(string) {
				t.Fatalf("snapshot=%+v intent=%+v err=%v", snapshot, committer.intent, err)
			}
			if test.wantError && store.failed == "" {
				t.Fatal("failed snapshot was not persisted before notification")
			}
			if !test.wantError && store.completed.Status != "succeeded" {
				t.Fatalf("completed snapshot=%+v", store.completed)
			}
		})
	}
}

func reportRefreshEdgeService(store *reportSnapshotStoreStub, compiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error), committer ReportSnapshotNotificationCommitter) *ReportApplicationService {
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{{Key: "empty", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "empty", Alias: "empty"}, Measures: []reportmodel.ReportDatasetMeasure{{Key: "count", Operation: "count"}}}, Materialization: &reportmodel.ReportMaterializationPolicy{MaximumLagSeconds: 60, ConsistencyRetries: 1}}}
		},
		Access: reportEmptyAccess{}, Records: reportEmptyRecords{}, Snapshots: store, SnapshotSources: reportSnapshotSourceStub{},
	})
	return NewReportApplicationService(ReportApplicationDependencies{Domain: domain, NotificationCompiler: compiler, NotificationCommitter: committer})
}

func TestReportRefreshRemainingNotificationCommitBoundaries(t *testing.T) {
	store := &reportSnapshotStoreStub{}
	if snapshot, err := reportRefreshEdgeService(store, nil, nil).RefreshSnapshot(t.Context(), "empty", "no-notification", reportPrincipal()); err != nil || snapshot.Status != "succeeded" || store.completed.Status != "succeeded" {
		t.Fatalf("snapshot=%+v completed=%+v err=%v", snapshot, store.completed, err)
	}
	compiler := func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{EventType: intent.EventType}, nil
	}
	if _, err := reportRefreshEdgeService(&reportSnapshotStoreStub{}, compiler, nil).RefreshSnapshot(t.Context(), "empty", "missing-committer", reportPrincipal()); apperror.CodeOf(err) != "backend.report.snapshot_notification_committer_unavailable" {
		t.Fatalf("committer err=%v", err)
	}
	compileErr := errors.New("compile")
	if _, err := reportRefreshEdgeService(&reportSnapshotStoreStub{}, func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, compileErr
	}, &reportNotificationCommitterStub{store: &reportSnapshotStoreStub{}}).RefreshSnapshot(t.Context(), "empty", "compile", reportPrincipal()); apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("compile err=%v", err)
	}
	commitErr := errors.New("commit")
	commitStore := &reportSnapshotStoreStub{}
	committer := &reportNotificationCommitterStub{store: commitStore, err: commitErr}
	if _, err := reportRefreshEdgeService(commitStore, compiler, committer).RefreshSnapshot(t.Context(), "empty", "notification-commit", reportPrincipal()); apperror.CodeOf(err) != "backend.report.snapshot_terminal_commit_failed" {
		t.Fatalf("notification commit err=%v", err)
	}
	domainStore := &reportSnapshotStoreStub{completeErr: commitErr}
	if _, err := reportRefreshEdgeService(domainStore, nil, nil).RefreshSnapshot(t.Context(), "empty", "domain-commit", reportPrincipal()); apperror.CodeOf(err) != "backend.report.snapshot_terminal_commit_failed" {
		t.Fatalf("domain commit err=%v", err)
	}
	service := NewReportApplicationService(ReportApplicationDependencies{})
	if _, notify, err := service.snapshotTerminalNotification("empty", "key", reportservice.ReportSnapshotRefresh{}, reportPrincipal()); err != nil || notify {
		t.Fatalf("nil compiler notify=%v err=%v", notify, err)
	}
	if got := reportSnapshotOccurredAt(reportmodel.ReportSnapshot{RefreshedAt: " refreshed "}); got != "refreshed" {
		t.Fatalf("occurred at=%q", got)
	}
}

func TestReportExportObjectAllowsNonApprovalControl(t *testing.T) {
	control := reportExportEdgeControl()
	service := reportExportEdgeService(&reportExportStoreStub{}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, time.Now())
	content, filename, err := service.ExportObject(t.Context(), "revenue", "customer", reportPrincipal())
	if err != nil || len(content) == 0 || filename == "" {
		t.Fatalf("content=%q filename=%q err=%v", content, filename, err)
	}
}
