package record

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-data-exchange-sdk/modulehost"
	"github.com/domainry/domainry-data-exchange/fileengine"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const recordDataExchangeResultChunkBytes = 1 << 20

// DataExchangeProviders is the Runtime-owned anti-corruption boundary exposed
// to either the in-process Module or the SaaS Binding. It contains no file/job
// persistence: Runtime remains responsible only for authorized Record domain
// validation, mutation and projection.
type DataExchangeProviders struct {
	mu              sync.Mutex
	importer        *RecordImportApplicationService
	exporter        *RecordExportApplicationService
	resolve         func(context.Context, string, string) principalmodel.Principal
	imports         map[string]*recordDataExchangeImportProvider
	exportProviders map[string]modulehost.ExportProvider
}

func NewDataExchangeProviders(resolve func(context.Context, string, string) principalmodel.Principal) *DataExchangeProviders {
	return &DataExchangeProviders{resolve: resolve, imports: map[string]*recordDataExchangeImportProvider{}, exportProviders: map[string]modulehost.ExportProvider{}}
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
	return dataExchangeImportProvider{owner: p}, strings.TrimSpace(key) == "records"
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

func (p dataExchangeImportProvider) ValidateImportBatch(ctx context.Context, b dataexchange.ImportBatch) (dataexchange.ImportBatchResult, error) {
	p.owner.mu.Lock()
	provider := p.owner.imports[b.JobID]
	if provider == nil && p.owner.importer != nil {
		provider = newRecordDataExchangeImportProvider(p.owner.importer, p.owner.principal(ctx, b.Scope))
		p.owner.imports[b.JobID] = provider
	}
	p.owner.mu.Unlock()
	if provider == nil {
		return dataexchange.ImportBatchResult{}, fmt.Errorf("Record import provider is unavailable")
	}
	return provider.ValidateImportBatch(ctx, b)
}
func (p dataExchangeImportProvider) ApplyImportBatch(ctx context.Context, b dataexchange.ImportBatch) (dataexchange.ImportBatchResult, error) {
	p.owner.mu.Lock()
	provider := p.owner.imports[b.JobID]
	delete(p.owner.imports, b.JobID)
	if provider == nil && p.owner.importer != nil {
		provider = newRecordDataExchangeImportProvider(p.owner.importer, p.owner.principal(ctx, b.Scope))
	}
	p.owner.mu.Unlock()
	if provider == nil {
		return dataexchange.ImportBatchResult{}, fmt.Errorf("Record import provider is unavailable")
	}
	return provider.ApplyImportBatch(ctx, b)
}

type dataExchangeExportProvider struct{ owner *DataExchangeProviders }

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
	encoded, err := exporter.encodeExportPage(ctx, prepared, pageNumber, true, recordDataExchangeResultChunkBytes)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	var columns []string
	rows := make([][]string, 0, encoded.rows)
	columns, err = fileengine.DecodeCSV(ctx, bytes.NewReader(encoded.content), fileengine.CSVDecodeLimits{MaxBytes: int64(len(encoded.content)), MaxRows: recordExportBatchSize, MaxColumns: recordImportMaxColumns}, func(_ []string, row fileengine.CSVRecord) error {
		rows = append(rows, row.Values)
		return nil
	})
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	next := ""
	if encoded.hasNext {
		next = "record-page:" + strconv.Itoa(pageNumber+1)
		return dataexchange.ExportPage{Columns: columns, Rows: rows, NextCursor: next, Total: pageNumber * recordExportBatchSize}, nil
	}
	return dataexchange.ExportPage{Columns: columns, Rows: rows, Total: (pageNumber-1)*recordExportBatchSize + len(rows)}, nil
}

var _ modulehost.Host = (*DataExchangeProviders)(nil)
