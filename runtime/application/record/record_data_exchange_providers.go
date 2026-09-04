package record

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-data-exchange-sdk/modulehost"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// DataExchangeProviders is the Runtime-owned anti-corruption boundary exposed
// to either the in-process Module or the SaaS Binding. It contains no file/job
// persistence: Runtime remains responsible only for authorized Record domain
// validation, mutation and projection.
type DataExchangeProviders struct {
	mu              sync.Mutex
	importer        *RecordImportApplicationService
	exporter        *RecordExportApplicationService
	resolve         func(context.Context, string, string) principalmodel.Principal
	imports         map[string]recordDataExchangeImportAttempt
	importProviders map[string]modulehost.ImportProvider
	exportProviders map[string]modulehost.ExportProvider
	notify          func(context.Context, notificationmodel.NotificationIntent) error
	now             func() time.Time
}

type recordDataExchangeImportAttempt struct {
	attempt  int
	provider *recordDataExchangeImportProvider
}

func NewDataExchangeProviders(resolve func(context.Context, string, string) principalmodel.Principal) *DataExchangeProviders {
	return &DataExchangeProviders{resolve: resolve, imports: map[string]recordDataExchangeImportAttempt{}, importProviders: map[string]modulehost.ImportProvider{}, exportProviders: map[string]modulehost.ExportProvider{}}
}

// RegisterImportProvider attaches an application-owned atomic or row-batch
// provider to the shared Module/SaaS host bridge.
func (p *DataExchangeProviders) RegisterImportProvider(key string, provider modulehost.ImportProvider) {
	if p == nil || strings.TrimSpace(key) == "" || provider == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.importProviders[strings.TrimSpace(key)] = provider
}

func (p *DataExchangeProviders) Bind(importer *RecordImportApplicationService, exporter *RecordExportApplicationService) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.importer, p.exporter = importer, exporter
}

func (p *DataExchangeProviders) ConfigureResolver(resolve func(context.Context, string, string) principalmodel.Principal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resolve = resolve
}

func (p *DataExchangeProviders) ConfigureExportNotifications(notify func(context.Context, notificationmodel.NotificationIntent) error, now func() time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.notify, p.now = notify, now
}

// RegisterExportProvider attaches an application-owned provider to the shared
// Module/SaaS host bridge. The registry owns no job or artifact state.
func (p *DataExchangeProviders) RegisterExportProvider(key string, provider modulehost.ExportProvider) {
	if p == nil || strings.TrimSpace(key) == "" || provider == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exportProviders[strings.TrimSpace(key)] = provider
}

func (p *DataExchangeProviders) ImportProvider(key string) (modulehost.ImportProvider, bool) {
	key = strings.TrimSpace(key)
	if key == "records" {
		return dataExchangeImportProvider{owner: p}, true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	provider, ok := p.importProviders[key]
	return provider, ok
}
func (p *DataExchangeProviders) ExportProvider(key string) (modulehost.ExportProvider, bool) {
	key = strings.TrimSpace(key)
	if key == "records" {
		return dataExchangeExportProvider{owner: p}, true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	provider, ok := p.exportProviders[key]
	return provider, ok
}

func (p *DataExchangeProviders) principal(ctx context.Context, scope dataexchange.Scope) principalmodel.Principal {
	value := principalmodel.Principal{}
	if p.resolve != nil {
		value = p.resolve(ctx, scope.ActorID, scope.RoleKey)
	}
	value.WorkspaceID, value.UserID, value.RequestID = scope.WorkspaceID, scope.ActorID, scope.RequestID
	return value
}

// ResolvePrincipal reconstructs the current persisted authorization context
// for an application-owned provider. Scope identity remains supplied by Data
// Exchange and cannot be overridden by provider payloads.
func (p *DataExchangeProviders) ResolvePrincipal(ctx context.Context, scope dataexchange.Scope) principalmodel.Principal {
	return p.principal(ctx, scope)
}

type dataExchangeImportProvider struct{ owner *DataExchangeProviders }

func (p dataExchangeImportProvider) ProjectDataExchangeJob(_ context.Context, job dataexchange.Job, scope dataexchange.Scope) (any, error) {
	return projectRecordDataExchangeJob(job, scope)
}

func (p dataExchangeImportProvider) ValidateImportBatch(ctx context.Context, b dataexchange.ImportBatch) (dataexchange.ImportBatchResult, error) {
	attempt := b.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	p.owner.mu.Lock()
	entry := p.owner.imports[b.JobID]
	if (entry.provider == nil || entry.attempt != attempt) && p.owner.importer != nil {
		entry = recordDataExchangeImportAttempt{attempt: attempt, provider: newRecordDataExchangeImportProvider(p.owner.importer, p.owner.principal(ctx, b.Scope))}
		p.owner.imports[b.JobID] = entry
	}
	p.owner.mu.Unlock()
	if entry.provider == nil {
		return dataexchange.ImportBatchResult{}, fmt.Errorf("Record import provider is unavailable")
	}
	result, err := entry.provider.ValidateImportBatch(ctx, b)
	if b.Final {
		p.owner.mu.Lock()
		if current := p.owner.imports[b.JobID]; current.provider == entry.provider {
			delete(p.owner.imports, b.JobID)
		}
		p.owner.mu.Unlock()
	}
	return result, err
}
func (p dataExchangeImportProvider) ApplyImportBatch(ctx context.Context, b dataexchange.ImportBatch) (dataexchange.ImportBatchResult, error) {
	p.owner.mu.Lock()
	importer := p.owner.importer
	p.owner.mu.Unlock()
	if importer == nil {
		return dataexchange.ImportBatchResult{}, fmt.Errorf("Record import provider is unavailable")
	}
	provider := newRecordDataExchangeImportProvider(importer, p.owner.principal(ctx, b.Scope))
	return provider.ApplyImportBatch(ctx, b)
}

type dataExchangeExportProvider struct{ owner *DataExchangeProviders }

func (p dataExchangeExportProvider) ProjectDataExchangeJob(_ context.Context, job dataexchange.Job, scope dataexchange.Scope) (any, error) {
	return projectRecordDataExchangeJob(job, scope)
}

func (p dataExchangeExportProvider) ReadExportPage(ctx context.Context, r dataexchange.ExportPageRequest) (dataexchange.ExportPage, error) {
	p.owner.mu.Lock()
	exporter := p.owner.exporter
	p.owner.mu.Unlock()
	if exporter == nil {
		return dataexchange.ExportPage{}, fmt.Errorf("Record export provider is unavailable")
	}
	var payload recordDataExchangeExportPayload
	if len(r.Options) > 0 {
		if err := json.Unmarshal(r.Options, &payload); err != nil {
			return dataexchange.ExportPage{}, fmt.Errorf("decode Record export options: %w", err)
		}
	}
	principal := p.owner.principal(ctx, r.Scope)
	object, fields, evidence, err := exporter.prepareExport(ctx, r.ObjectKey, principal, payload.Options, payload.AssuranceEvidence, true)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	prepared := recordExportPrepared{object: object, fields: fields, evidence: evidence, options: payload.Options, principal: principal}
	cursor, err := decodeRecordExportCursor(r.Cursor)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	total := cursor.Total
	if strings.TrimSpace(r.Cursor) == "" {
		total, err = exporter.countPrepared(ctx, prepared)
		if err != nil {
			return dataexchange.ExportPage{}, err
		}
	}
	page, err := exporter.projectExportPage(ctx, prepared, cursor.AfterID)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	next := ""
	if page.hasNext {
		next = encodeRecordExportCursor(recordExportCursor{AfterID: page.next, Total: total})
	}
	return dataexchange.ExportPage{Columns: page.columns, Rows: page.rows, NextCursor: next, Total: total}, nil
}

func (p dataExchangeExportProvider) CompleteExport(ctx context.Context, completion dataexchange.ExportCompletion) error {
	p.owner.mu.Lock()
	notify, now := p.owner.notify, p.owner.now
	p.owner.mu.Unlock()
	if notify == nil {
		return nil
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	identity := sha256.Sum256([]byte(completion.JobID + ":completed"))
	sourceEventID := "record-export:" + completion.JobID + ":completed"
	expiresAt := ""
	if !completion.Artifact.ExpiresAt.IsZero() {
		expiresAt = completion.Artifact.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	return notify(ctx, notificationmodel.NotificationIntent{
		ID: "notification_record_export_" + hex.EncodeToString(identity[:12]), WorkspaceID: completion.Scope.WorkspaceID,
		SourceEventID: sourceEventID, EventType: "record.export.completed",
		RecipientUserIDs: []string{completion.Scope.ActorID}, SubjectType: "record_export", SubjectID: completion.JobID,
		SubjectVersion: completion.Artifact.SHA256, DedupeKey: sourceEventID, ActionState: notificationmodel.NotificationActionOpen,
		ExpiresAt: expiresAt, OccurredAt: now().UTC().Format(time.RFC3339Nano),
		Variables: map[string]any{"object_key": completion.ObjectKey, "filename": completion.Artifact.Filename, "row_count": completion.Rows, "status": "completed"},
	})
}

type recordExportCursor struct {
	AfterID string `json:"after_id"`
	Total   int    `json:"total"`
}

func encodeRecordExportCursor(cursor recordExportCursor) string {
	value, _ := json.Marshal(cursor)
	return "record-id:" + base64.RawURLEncoding.EncodeToString(value)
}

func decodeRecordExportCursor(cursor string) (recordExportCursor, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return recordExportCursor{}, nil
	}
	encoded := strings.TrimPrefix(cursor, "record-id:")
	if encoded == cursor || encoded == "" {
		return recordExportCursor{}, fmt.Errorf("invalid Record export cursor")
	}
	value, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return recordExportCursor{}, fmt.Errorf("invalid Record export cursor")
	}
	var decoded recordExportCursor
	if err := json.Unmarshal(value, &decoded); err != nil || strings.TrimSpace(decoded.AfterID) == "" || decoded.Total < 0 {
		return recordExportCursor{}, fmt.Errorf("invalid Record export cursor")
	}
	return decoded, nil
}

func projectRecordDataExchangeJob(job dataexchange.Job, scope dataexchange.Scope) (any, error) {
	if job.Provider != "records" || job.WorkspaceID != strings.TrimSpace(scope.WorkspaceID) || job.ActorID != strings.TrimSpace(scope.ActorID) {
		return nil, dataexchange.ErrJobNotFound
	}
	return recordBatchJobFromDataExchange(job), nil
}

var _ modulehost.Host = (*DataExchangeProviders)(nil)
var _ modulehost.JobProjector = dataExchangeImportProvider{}
var _ modulehost.JobProjector = dataExchangeExportProvider{}
var _ modulehost.ExportCompletionProvider = dataExchangeExportProvider{}
