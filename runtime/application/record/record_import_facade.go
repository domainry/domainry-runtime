package record

import (
	"context"
	"fmt"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"strings"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

func (s *RecordApplicationService) PlanCreateMutation(ctx context.Context, objectKey string, data map[string]any, recordID string, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
	if err := s.validateProfileBindingMutation(objectKey, data, true); err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	return s.create.PlanCreateMutation(ctx, objectKey, data, recordID, principal)
}

func (s *RecordApplicationService) PlanUpdateMutation(ctx context.Context, objectKey, recordID string, patch map[string]any, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
	if err := s.validateProfileBindingMutation(objectKey, patch, false); err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	return s.update.PlanUpdateMutation(ctx, objectKey, recordID, patch, principal)
}

func (s *RecordUpdateApplicationService) PlanUpdateMutation(ctx context.Context, objectKey, recordID string, patch map[string]any, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	authorizationPrincipal := recordEffectAuthorizationPrincipal(ctx, principal, objectKey, "update")
	object, err := s.dependencies.ObjectForAction(authorizationPrincipal, objectKey, "update")
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	if err := recordpolicy.RecordValidateSchedulerOperationalCRUD(object, "update"); err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	record, found, err := s.dependencies.Repository.GetRecord(ctx, principal.WorkspaceID, object, recordID)
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordUpdateError(apperror.KindInternal, "backend.internal", err, "operation", "get record")
	}
	if !found {
		if planned := recordservice.RecordPlannedRelations(ctx); planned[objectKey] != nil {
			record, found = planned[objectKey][recordID]
		}
	}
	if !found {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordUpdateError(apperror.KindNotFound, "backend.record.not_found", nil)
	}
	allowed, err := s.canAccessScope(ctx, authorizationPrincipal, object, record, false)
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	if !allowed {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordUpdateError(apperror.KindForbidden, "backend.record.outside_scope", nil)
	}
	planned, err := s.planUpdate(ctx, objectKey, object, record, patch, nil, nil, principal, authorizationPrincipal)
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	return planned.plan, planned.record, nil
}

func (s *RecordApplicationService) EnqueueImportJob(ctx context.Context, objectKey string, rawCSV []byte, key string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	return s.batchJobs.EnqueueImport(ctx, objectKey, rawCSV, key, principal)
}

func (s *RecordApplicationService) EnqueueExportJob(ctx context.Context, objectKey, key string, options RecordExportOptions, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	return s.batchJobs.EnqueueExport(ctx, objectKey, key, options, principal)
}

func (s *RecordApplicationService) RegisterOwnedBatchProcessor(kind string, processor RecordBatchOwnedProcessor) error {
	return s.batchJobs.RegisterOwnedProcessor(kind, processor)
}

func (s *RecordApplicationService) EnqueueOwnedBatchJob(ctx context.Context, kind, objectKey, key, auditID string, payload []byte, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	return s.batchJobs.EnqueueOwned(ctx, kind, objectKey, key, auditID, payload, principal)
}

func (s *RecordApplicationService) GetOwnedBatchJob(ctx context.Context, jobID, kind string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	return s.batchJobs.GetOwned(ctx, jobID, kind, principal)
}

func (s *RecordApplicationService) FindOwnedBatchJobByFingerprint(ctx context.Context, kind, objectKey, fingerprint string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	return s.batchJobs.FindOwnedByFingerprint(ctx, kind, objectKey, fingerprint, principal)
}

func (s *RecordApplicationService) FindOwnedBatchJobByIdempotency(ctx context.Context, kind, objectKey, key string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	return s.batchJobs.FindOwnedByIdempotency(ctx, kind, objectKey, key, principal)
}

func (s *RecordApplicationService) CancelOwnedBatchJob(ctx context.Context, jobID, kind string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	if _, err := s.batchJobs.GetOwned(ctx, jobID, kind, principal); err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	return s.batchJobs.Cancel(ctx, jobID, principal)
}

func (s *RecordApplicationService) GetBatchJob(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	return s.batchJobs.Get(ctx, jobID, principal)
}

func (s *RecordApplicationService) CancelBatchJob(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	return s.batchJobs.Cancel(ctx, jobID, principal)
}

func (s *RecordApplicationService) DownloadBatchJob(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, []recordmodel.RecordBatchJobChunk, error) {
	return s.batchJobs.Download(ctx, jobID, principal)
}

func (s *RecordApplicationService) StartBatchJobWorker(ctx context.Context, interval time.Duration, limit int) <-chan struct{} {
	return s.batchJobs.StartWorker(ctx, interval, limit)
}

func (s *RecordApplicationService) ProcessDueBatchJobs(ctx context.Context, limit int) error {
	return s.batchJobs.ProcessDue(ctx, limit)
}

func (s *RecordApplicationService) BatchJobOpenMetrics(context.Context) string {
	if s == nil || s.batchJobs == nil {
		return ""
	}
	b := s.batchJobs
	return fmt.Sprintf("# HELP domainry_runtime_record_batch_jobs_total Record batch jobs by transition.\n# TYPE domainry_runtime_record_batch_jobs_total counter\ndomainry_runtime_record_batch_jobs_total{outcome=\"queued\"} %d\ndomainry_runtime_record_batch_jobs_total{outcome=\"completed\"} %d\ndomainry_runtime_record_batch_jobs_total{outcome=\"failed\"} %d\ndomainry_runtime_record_batch_jobs_total{outcome=\"quarantined\"} %d\ndomainry_runtime_record_batch_jobs_total{outcome=\"cancelled\"} %d\ndomainry_runtime_record_batch_jobs_total{outcome=\"retried\"} %d\n# HELP domainry_runtime_record_batch_in_flight Current record batch jobs.\n# TYPE domainry_runtime_record_batch_in_flight gauge\ndomainry_runtime_record_batch_in_flight %d\n# HELP domainry_runtime_record_batch_queue_depth Queued record batch jobs.\n# TYPE domainry_runtime_record_batch_queue_depth gauge\ndomainry_runtime_record_batch_queue_depth %d\n# HELP domainry_runtime_record_batch_oldest_seconds Age of oldest queued job.\n# TYPE domainry_runtime_record_batch_oldest_seconds gauge\ndomainry_runtime_record_batch_oldest_seconds %.3f\n", b.queued.Load(), b.completed.Load(), b.failed.Load(), b.quarantined.Load(), b.cancelled.Load(), b.retried.Load(), b.inFlight.Load(), b.queueDepth.Load(), float64(b.oldestMillis.Load())/1000)
}

func (s *RecordApplicationService) InspectBatchJobDeadLetter(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	return s.batchJobs.InspectTerminal(ctx, jobID, principal)
}

func (s *RecordApplicationService) RetryBatchJobDeadLetter(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	return s.batchJobs.RetryTerminal(ctx, jobID, principal)
}

func (s *RecordApplicationService) ResolveBatchJobDeadLetter(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	return s.batchJobs.ResolveTerminal(ctx, jobID, principal)
}

func bytesCountCSVRows(content []byte) int {
	count := 0
	for _, value := range content {
		if value == '\n' {
			count++
		}
	}
	if count > 0 {
		count--
	}
	return count
}

func (s *RecordApplicationService) PreviewImport(ctx context.Context, objectKey string, rawCSV []byte, principal principalmodel.Principal) (recordmodel.RecordImportPreview, error) {
	return s.importer.Preview(ctx, objectKey, rawCSV, principal)
}

func (s *RecordApplicationService) ApplyImport(ctx context.Context, objectKey string, rawCSV []byte, principal principalmodel.Principal) (recordmodel.RecordImportApplyResult, error) {
	return s.importer.Apply(ctx, objectKey, rawCSV, principal)
}

func (s *RecordApplicationService) ApplyImportIdempotent(ctx context.Context, objectKey string, rawCSV []byte, operationKey string, principal principalmodel.Principal) (recordmodel.RecordImportApplyResult, bool, error) {
	return s.importer.ApplyIdempotent(ctx, objectKey, rawCSV, operationKey, principal)
}

func (s *RecordApplicationService) CreateRecord(ctx context.Context, objectKey string, data map[string]any, principal principalmodel.Principal) (record recordmodel.Record, err error) {
	ctx, span := telemetry.StartUseCase(ctx, "record.create", attribute.String("object.key", objectKey))
	defer func() { telemetry.EndUseCase(span, err, "") }()
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if err := s.validateProfileBindingMutation(objectKey, data, true); err != nil {
		return recordmodel.Record{}, err
	}
	return s.create.Create(ctx, objectKey, data, principal)
}

func (s *RecordApplicationService) CreateRecordIdempotent(ctx context.Context, objectKey string, data map[string]any, idempotencyKey string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if err := s.validateProfileBindingMutation(objectKey, data, true); err != nil {
		return recordmodel.Record{}, err
	}
	return s.create.CreateIdempotent(ctx, objectKey, data, idempotencyKey, principal)
}

func (s *RecordApplicationService) CreateRecordIdempotentResult(ctx context.Context, objectKey string, data map[string]any, idempotencyKey string, principal principalmodel.Principal) (recordmodel.Record, bool, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, false, err
	}
	if err := s.validateProfileBindingMutation(objectKey, data, true); err != nil {
		return recordmodel.Record{}, false, err
	}
	object, err := s.queryPolicy.ObjectForAction(principal, objectKey, "create")
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	if s.recordMutationExecution == nil {
		return recordmodel.Record{}, false, apperror.New(apperror.KindInternal, "backend.idempotency.receipt_unavailable", nil, nil)
	}
	replay, claim, found, err := s.recordMutationExecution.BeginCreate(ctx, objectKey, idempotencyKey, recordvalidation.RecordCloneData(data), principal)
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	if found {
		return recordpolicy.RecordFilterReadable(principal, object, replay), true, nil
	}
	created, err := s.create.CreateClaimed(ctx, objectKey, data, claim, principal)
	return created, false, err
}

func (s *RecordApplicationService) CreateLocalizedRecordIdempotentResult(ctx context.Context, objectKey string, data map[string]any, translations recordmodel.RecordTranslations, idempotencyKey string, principal principalmodel.Principal) (recordmodel.Record, bool, error) {
	if len(translations) == 0 {
		return s.CreateRecordIdempotentResult(ctx, objectKey, data, idempotencyKey, principal)
	}
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, false, err
	}
	if err := s.validateProfileBindingMutation(objectKey, data, true); err != nil {
		return recordmodel.Record{}, false, err
	}
	object, err := s.queryPolicy.ObjectForAction(principal, objectKey, "create")
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	localizedValues, err := recordmodel.RecordNormalizeTranslations(object, translations)
	if err != nil {
		return recordmodel.Record{}, false, apperror.New(apperror.KindBadRequest, "backend.record.localized_values_invalid", err, map[string]string{"detail": err.Error()})
	}
	if s.recordMutationExecution == nil {
		return recordmodel.Record{}, false, apperror.New(apperror.KindInternal, "backend.idempotency.receipt_unavailable", nil, nil)
	}
	fingerprintInput := recordvalidation.RecordCloneData(data)
	fingerprintInput["__localized_values"] = localizedValues
	replay, claim, found, err := s.recordMutationExecution.BeginCreate(ctx, objectKey, idempotencyKey, fingerprintInput, principal)
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	if found {
		return recordpolicy.RecordFilterReadable(principal, object, replay), true, nil
	}
	created, err := s.create.CreateClaimedLocalized(ctx, objectKey, data, translations, claim, principal)
	return created, false, err
}

func (s *RecordApplicationService) UpdateRecord(ctx context.Context, objectKey, recordID string, patch map[string]any, principal principalmodel.Principal) (record recordmodel.Record, err error) {
	ctx, span := telemetry.StartUseCase(ctx, "record.update", attribute.String("object.key", objectKey))
	defer func() { telemetry.EndUseCase(span, err, "") }()
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if err := s.validateProfileBindingMutation(objectKey, patch, false); err != nil {
		return recordmodel.Record{}, err
	}
	return s.update.Update(ctx, objectKey, recordID, patch, principal)
}

func (s *RecordApplicationService) UpdateRecordIdempotent(ctx context.Context, objectKey, recordID string, patch map[string]any, idempotencyKey string, principal principalmodel.Principal) (record recordmodel.Record, err error) {
	ctx, span := telemetry.StartUseCase(ctx, "record.update", attribute.String("object.key", objectKey))
	defer func() { telemetry.EndUseCase(span, err, "") }()
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if err := s.validateProfileBindingMutation(objectKey, patch, false); err != nil {
		return recordmodel.Record{}, err
	}
	return s.update.UpdateIdempotent(ctx, objectKey, recordID, patch, idempotencyKey, principal)
}

func (s *RecordApplicationService) UpdateLocalizedRecord(ctx context.Context, objectKey, recordID string, patch map[string]any, translations recordmodel.RecordTranslations, principal principalmodel.Principal) (recordmodel.Record, error) {
	if len(translations) == 0 {
		return s.UpdateRecord(ctx, objectKey, recordID, patch, principal)
	}
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if err := s.validateProfileBindingMutation(objectKey, patch, false); err != nil {
		return recordmodel.Record{}, err
	}
	return s.update.UpdateLocalized(ctx, objectKey, recordID, patch, translations, principal)
}

func (s *RecordApplicationService) UpdateLocalizedRecordIdempotent(ctx context.Context, objectKey, recordID string, patch map[string]any, translations recordmodel.RecordTranslations, idempotencyKey string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if len(translations) == 0 {
		return s.UpdateRecordIdempotent(ctx, objectKey, recordID, patch, idempotencyKey, principal)
	}
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if err := s.validateProfileBindingMutation(objectKey, patch, false); err != nil {
		return recordmodel.Record{}, err
	}
	return s.update.UpdateLocalizedIdempotent(ctx, objectKey, recordID, patch, translations, idempotencyKey, principal)
}

func (s *RecordApplicationService) ConditionalUpdateRecord(ctx context.Context, objectKey, recordID string, input transactionmodel.ConditionalUpdateInput, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if err := s.validateProfileBindingMutation(objectKey, input.Patch, false); err != nil {
		return recordmodel.Record{}, err
	}
	return s.update.ConditionalUpdate(ctx, objectKey, recordID, input, principal)
}

func (s *RecordApplicationService) validateProfileBindingMutation(objectKey string, values map[string]any, creating bool) error {
	if s == nil || s.identityProfileExtensions == nil || len(values) == 0 {
		return nil
	}
	for _, extension := range s.identityProfileExtensions() {
		field := strings.TrimSpace(extension.IdentityRelationField)
		if strings.TrimSpace(extension.ObjectKey) != strings.TrimSpace(objectKey) || !extension.BindingLifecycle.AllowUnbound || field == "" {
			continue
		}
		value, present := values[field]
		if !present {
			continue
		}
		if creating && (value == nil || strings.TrimSpace(fmt.Sprint(value)) == "") {
			continue
		}
		return apperror.New(apperror.KindForbidden, "backend.identity.profile_binding_command_required", nil, map[string]string{
			"object_key": objectKey,
			"field_key":  field,
		})
	}
	return nil
}

func (s *RecordApplicationService) DeleteRecord(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (err error) {
	ctx, span := telemetry.StartUseCase(ctx, "record.delete", attribute.String("object.key", objectKey))
	defer func() { telemetry.EndUseCase(span, err, "") }()
	if err := recordAuthorizeCommand(principal); err != nil {
		return err
	}
	return s.delete.Delete(ctx, objectKey, recordID, principal)
}

func (s *RecordApplicationService) DeleteRecordExpected(ctx context.Context, objectKey, recordID, expectedUpdatedAt string, principal principalmodel.Principal) error {
	if err := recordAuthorizeCommand(principal); err != nil {
		return err
	}
	return s.delete.DeleteExpected(ctx, objectKey, recordID, expectedUpdatedAt, principal)
}

func (s *RecordApplicationService) RestoreRecord(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, err
	}
	return s.restore.Restore(ctx, objectKey, recordID, principal)
}

func (s *RecordApplicationService) ExportRecords(ctx context.Context, objectKey string, principal principalmodel.Principal) ([]byte, string, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return nil, "", err
	}
	return s.exporter.Export(ctx, objectKey, principal)
}

func (s *RecordApplicationService) ExportRecordsWithOptions(ctx context.Context, objectKey string, principal principalmodel.Principal, options RecordExportOptions) ([]byte, string, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return nil, "", err
	}
	return s.exporter.ExportWithOptions(ctx, objectKey, principal, options)
}

func (s *RecordApplicationService) RebuildOwnerDepartmentPaths(ctx context.Context, workspaceID string, workforce []identitysdk.WorkforceEntry) (int, error) {
	if _, err := principalmodel.NewWorkspaceCommandScope(workspaceID); err != nil {
		return 0, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return s.ownerDepartmentPaths.Rebuild(ctx, workspaceID, workforce)
}
