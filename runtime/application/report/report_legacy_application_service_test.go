package report

import (
	"context"
	"strings"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportadapter "github.com/domainry/domainry-runtime/runtime/application/report/adapter"
	reportexportapplication "github.com/domainry/domainry-runtime/runtime/application/report/export/application"
	reportquery "github.com/domainry/domainry-runtime/runtime/application/report/query"
	reportsnapshot "github.com/domainry/domainry-runtime/runtime/application/report/snapshot"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/query"
	reportsnapshotdomain "github.com/domainry/domainry-runtime/runtime/domain/report/snapshot"
)

type ReportQueryApplicationService = reportquery.ReportQueryApplicationService
type ReportQueryApplicationDependencies = reportquery.ReportQueryApplicationDependencies
type ReportSnapshotApplicationService = reportsnapshot.ReportSnapshotApplicationService
type ReportSnapshotApplicationDependencies = reportsnapshot.ReportSnapshotApplicationDependencies
type ReportSnapshotNotificationCommitter = reportsnapshot.ReportSnapshotNotificationCommitter
type ReportRecordAdapter = reportadapter.ReportRecordAdapter
type ReportExportApplicationService = reportexportapplication.ReportExportApplicationService
type ReportExportApplicationDependencies = reportexportapplication.ReportExportApplicationDependencies
type ReportExportRecordStore = reportexportapplication.ReportExportRecordStore
type ReportExportPreparation = reportexportapplication.ReportExportPreparation

var NewReportQueryApplicationService = reportquery.NewReportQueryApplicationService
var NewReportSnapshotApplicationService = reportsnapshot.NewReportSnapshotApplicationService
var NewReportRecordAdapter = reportadapter.NewReportRecordAdapter
var NewReportExportApplicationService = reportexportapplication.NewReportExportApplicationService

// Test-only compatibility fixture for older cross-capability tests. Production
// composition uses the three capability-specific application services.
type ReportApplicationDependencies struct {
	ProductBrandName      string
	Domain                *reportservice.ReportDomainService
	ExportRecords         ReportExportRecordStore
	Audit                 auditcontract.AuditAppender
	NotificationCompiler  func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	NotificationCommitter ReportSnapshotNotificationCommitter
	ExportControls        func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema
	Clock                 func() time.Time
	DataExchange          dataexchange.Binding
	DataExchangeProviders *recordapplication.DataExchangeProviders
	CursorKey             []byte
}

type ReportApplicationService struct {
	queries                          *ReportQueryApplicationService
	snapshots                        *ReportSnapshotApplicationService
	exports                          *ReportExportApplicationService
	snapshotNotificationDependencies struct {
		compiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	}
}

func NewReportApplicationService(dependencies ReportApplicationDependencies) *ReportApplicationService {
	cursorKey := append([]byte(nil), dependencies.CursorKey...)
	if len(cursorKey) == 0 {
		cursorKey = []byte("report-pagination-test-key")
	}
	service := &ReportApplicationService{
		queries: NewReportQueryApplicationService(ReportQueryApplicationDependencies{Domain: dependencies.Domain, CursorKey: cursorKey}),
		snapshots: NewReportSnapshotApplicationService(ReportSnapshotApplicationDependencies{
			Domain: dependencies.Domain, NotificationCompiler: dependencies.NotificationCompiler, NotificationCommitter: dependencies.NotificationCommitter,
		}),
		exports: NewReportExportApplicationService(ReportExportApplicationDependencies{
			ProductBrandName: dependencies.ProductBrandName, Domain: dependencies.Domain, Records: dependencies.ExportRecords,
			Audit: dependencies.Audit, Controls: dependencies.ExportControls, Clock: dependencies.Clock,
			DataExchange: dependencies.DataExchange, DataExchangeProviders: dependencies.DataExchangeProviders,
		}),
	}
	service.snapshotNotificationDependencies.compiler = dependencies.NotificationCompiler
	return service
}

func (s *ReportApplicationService) Summary(ctx context.Context, key string, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	return s.queries.Summary(ctx, key, principal)
}
func (s *ReportApplicationService) SummaryMode(ctx context.Context, key, mode string, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	return s.queries.SummaryMode(ctx, key, mode, principal)
}
func (s *ReportApplicationService) QueryObjectSQL(ctx context.Context, key string, parameters map[string]any, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	return s.queries.QueryObjectSQL(ctx, key, parameters, principal)
}
func (s *ReportApplicationService) QueryObjectSQLPage(ctx context.Context, key string, parameters map[string]any, page reportmodel.ReportPageRequest, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	return s.queries.QueryObjectSQLPage(ctx, key, parameters, page, principal)
}
func (s *ReportApplicationService) RefreshSnapshot(ctx context.Context, key, idempotencyKey string, principal principalmodel.Principal) (reportmodel.ReportSnapshot, error) {
	return s.snapshots.RefreshSnapshot(ctx, key, idempotencyKey, principal)
}
func (s *ReportApplicationService) snapshotTerminalNotification(key, idempotencyKey string, refresh reportsnapshotdomain.ReportSnapshotRefresh, principal principalmodel.Principal) (notificationmodel.NotificationEvent, bool, error) {
	dependencies := s.snapshotNotificationDependencies
	if dependencies.compiler == nil || principal.UserID == "" {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	status := "completed"
	if refresh.TerminalStatus == "failed" {
		status = "failed"
	}
	key, idempotencyKey = strings.TrimSpace(key), strings.TrimSpace(idempotencyKey)
	sourceID := "report-snapshot:" + key + ":" + idempotencyKey + ":" + status
	event, err := dependencies.compiler(notificationmodel.NotificationIntent{
		WorkspaceID: principal.WorkspaceID, SourceEventID: sourceID, EventType: "report.snapshot." + status,
		Surface: "business_workspace", RecipientUserIDs: []string{principal.UserID}, SubjectType: "report", SubjectID: key,
		DedupeKey: sourceID, OccurredAt: reportSnapshotOccurredAt(refresh.Snapshot),
		Variables: map[string]any{"report_key": key, "status": status, "error_code": refresh.ErrorCode},
	})
	return event, true, err
}

func reportSnapshotOccurredAt(snapshot reportmodel.ReportSnapshot) string {
	if value := strings.TrimSpace(snapshot.RefreshedAt); value != "" {
		return value
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}
func (s *ReportApplicationService) PrepareExportRouted(ctx context.Context, reportKey, objectKey, auditID, idempotencyKey string, scope reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (ReportExportPreparation, error) {
	return s.exports.PrepareExportRouted(ctx, reportKey, objectKey, auditID, idempotencyKey, scope, principal)
}
