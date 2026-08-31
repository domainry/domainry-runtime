package record

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-data-exchange-sdk/modulehost"
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
	pageNumber := 1
	if r.Cursor != "" {
		pageNumber, err = strconv.Atoi(strings.TrimPrefix(r.Cursor, "record-page:"))
		if err != nil || pageNumber < 2 {
			return dataexchange.ExportPage{}, fmt.Errorf("invalid Record export cursor")
		}
	}
	page, err := exporter.projectExportPage(ctx, prepared, pageNumber)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	next := ""
	if page.hasNext {
		next = "record-page:" + strconv.Itoa(pageNumber+1)
		return dataexchange.ExportPage{Columns: page.columns, Rows: page.rows, NextCursor: next, Total: pageNumber * recordExportBatchSize}, nil
	}
	return dataexchange.ExportPage{Columns: page.columns, Rows: page.rows, Total: (pageNumber-1)*recordExportBatchSize + len(page.rows)}, nil
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
