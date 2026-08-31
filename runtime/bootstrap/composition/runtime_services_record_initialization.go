package composition

import (
	"context"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordruntime "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	reportadapter "github.com/domainry/domainry-runtime/runtime/application/report/adapter"
	reportexportapplication "github.com/domainry/domainry-runtime/runtime/application/report/export/application"
	reportquery "github.com/domainry/domainry-runtime/runtime/application/report/query"
	reportsnapshot "github.com/domainry/domainry-runtime/runtime/application/report/snapshot"
	surfacecontextbusiness "github.com/domainry/domainry-runtime/runtime/application/surfacecontext"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordexecutionruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportbusiness "github.com/domainry/domainry-runtime/runtime/domain/report/query"
)

type recordRuntimeState struct {
	recordMutations                       *recordruntime.RecordMutationApplicationService
	mutationKernel                        *recordruntime.MutationKernelApplicationService
	RecordMutationExecutionRuntime        *recordexecutionruntime.RecordMutationExecutionRuntime
	RecordScopeOwnerFactDerivationService *recordservice.RecordScopeOwnerFactDerivationDomainService
}

func (s *runtimeAssembly) reportRecordSchemaMap() map[string]definitionmodel.ObjectSchema {
	return schemaObjectMap(s.Schema().Objects)
}

func initializeRecordApplications(s *runtimeAssembly) {
	dependencies := buildRecordApplicationDependencies(s)
	s.mutationKernel = dependencies.MutationKernel
	s.recordApplicationService = recordapplication.NewRecordApplicationService(dependencies)
	s.RecordDomainService = s.recordApplicationService.RecordDomainService
	s.auditApplicationService.ConfigureBusinessExport(s.auditExportTokenKey, func(ctx context.Context, filters auditmodel.ExportFilter, principal principalmodel.Principal) error {
		if filters.ObjectKey == "" || filters.RecordID == "" {
			return nil
		}
		_, err := s.recordApplicationService.GetRecord(ctx, filters.ObjectKey, filters.RecordID, principal)
		return err
	})
	s.auditApplicationService.SetEventProjector(func(ctx context.Context, events []auditmodel.AuditEvent, principal principalmodel.Principal) ([]auditmodel.AuditEvent, error) {
		objects := s.reportRecordSchemaMap()
		projected := append([]auditmodel.AuditEvent(nil), events...)
		for index := range projected {
			object, exists := objects[projected[index].ObjectKey]
			if !exists || projected[index].RecordID == "" {
				continue
			}
			if projected[index].Before != nil {
				records, err := s.recordApplicationService.ProjectRecordFields(ctx, principal, object, []recordmodel.Record{{ID: projected[index].RecordID, Data: projected[index].Before}}, "audit")
				if err != nil {
					return nil, err
				}
				projected[index].Before = records[0].Data
			}
			if projected[index].After != nil {
				records, err := s.recordApplicationService.ProjectRecordFields(ctx, principal, object, []recordmodel.Record{{ID: projected[index].RecordID, Data: projected[index].After}}, "audit")
				if err != nil {
					return nil, err
				}
				projected[index].After = records[0].Data
			}
		}
		return projected, nil
	})
	s.recordMutations = recordruntime.NewRecordMutationApplicationService(s.recordApplicationService)
	reportRecords := reportadapter.NewReportRecordAdapter(s.recordApplicationService, s.recordRepo, s.reportRecordSchemaMap)
	reportDomain := reportbusiness.NewReportDomainService(reportbusiness.ReportDependencies{
		Reports: func(_ context.Context, principal principalmodel.Principal) []reportmodel.ReportSchema {
			return appschemaservice.SnapshotForPrincipal(s.Schema(), principal).Reports
		},
		Access: reportRecords, Records: reportRecords, DatasetRows: s.reportDatasetRows, ObjectSQL: s.reportObjectSQL,
		Snapshots: s.reportSnapshots, SnapshotSources: s.reportSnapshotSources,
	})
	s.reportSnapshotsService = reportsnapshot.NewReportSnapshotApplicationService(reportsnapshot.ReportSnapshotApplicationDependencies{
		Domain: reportDomain, NotificationCompiler: s.reportNotificationCompiler,
		NotificationCommitter: s.reportSnapshotNotificationCommitter,
	})
	s.reportQueriesService = reportquery.NewReportQueryApplicationService(reportquery.ReportQueryApplicationDependencies{
		Domain: reportDomain, CursorKey: s.auditExportTokenKey, Audit: reportadapter.NewReportCrossWorkspaceAuditAdapter(s.auditApplicationService),
	})
	s.reportExportsService = reportexportapplication.NewReportExportApplicationService(reportexportapplication.ReportExportApplicationDependencies{
		ProductBrandName: s.productBrandName, Domain: reportDomain, Records: reportRecords, Audit: s.auditApplicationService,
		DataExchange: s.dataExchange, DataExchangeProviders: s.dataExchangeProviders,
		Controls: func(_ context.Context, principal principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return append([]reportmodel.ReportExportControlSchema(nil), s.reportExportControls...)
		},
	})
	s.schedulerService.UseReportSnapshotRuntime(s.reportSnapshotsService)
	s.surfaceContextService = surfacecontextbusiness.NewSurfaceContextApplicationService(surfacecontextbusiness.SurfaceContextDependencies{
		Objects:     func() map[string]definitionmodel.ObjectSchema { return schemaObjectMap(s.Schema().Objects) },
		Reports:     func() []reportmodel.ReportSchema { return s.Schema().Reports },
		ListRecords: s.recordApplicationService.ListRecords,
		GetRecord:   s.recordApplicationService.GetRecord,
		ListStoredRecords: func(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			return listRuntimeSurfaceContextStoredRecords(ctx, s, workspaceID, object, query)
		},
		ListDirectoryUsers: func(ctx context.Context) ([]identitysdk.User, error) {
			return listRuntimeSurfaceContextDirectoryUsers(ctx, s)
		},
	})
}
