package record

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
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
	mu       sync.Mutex
	importer *RecordImportApplicationService
	exporter *RecordExportApplicationService
	resolve  func(context.Context, string, string) principalmodel.Principal
	imports  map[string]*recordDataExchangeImportProvider
}

func NewDataExchangeProviders(resolve func(context.Context, string, string) principalmodel.Principal) *DataExchangeProviders {
	return &DataExchangeProviders{resolve: resolve, imports: map[string]*recordDataExchangeImportProvider{}}
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

func (p *DataExchangeProviders) ImportProvider(key string) (modulehost.ImportProvider, bool) {
	return dataExchangeImportProvider{owner: p}, strings.TrimSpace(key) == "records"
}
func (p *DataExchangeProviders) ExportProvider(key string) (modulehost.ExportProvider, bool) {
	return dataExchangeExportProvider{owner: p}, strings.TrimSpace(key) == "records"
}

func (p *DataExchangeProviders) principal(ctx context.Context, scope dataexchange.Scope) principalmodel.Principal {
	value := principalmodel.Principal{}
	if p.resolve != nil {
		value = p.resolve(ctx, scope.ActorID, scope.RoleKey)
	}
	value.WorkspaceID, value.UserID, value.RequestID = scope.WorkspaceID, scope.ActorID, scope.RequestID
	return value
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
	var payload recordBatchExportPayload
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
	encoded, err := exporter.encodeExportPage(ctx, prepared, pageNumber, true, recordBatchResultChunkBytes)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	reader := csv.NewReader(bytes.NewReader(encoded.content))
	columns, err := reader.Read()
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	rows := make([][]string, 0, encoded.rows)
	for {
		row, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return dataexchange.ExportPage{}, readErr
		}
		rows = append(rows, row)
	}
	next := ""
	if encoded.hasNext {
		next = "record-page:" + strconv.Itoa(pageNumber+1)
		return dataexchange.ExportPage{Columns: columns, Rows: rows, NextCursor: next, Total: pageNumber * recordExportBatchSize}, nil
	}
	return dataexchange.ExportPage{Columns: columns, Rows: rows, Total: (pageNumber-1)*recordExportBatchSize + len(rows)}, nil
}

var _ modulehost.Host = (*DataExchangeProviders)(nil)
