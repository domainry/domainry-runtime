package record

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-data-exchange-sdk/modulehost"
	fileexchange "github.com/domainry/domainry-data-exchange/fileengine"
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

// recordDataExchangeImportProvider is the anti-corruption boundary between the
// generic file engine and Record-owned import semantics. One instance serves
// one job, so cross-batch validation state is isolated and discarded after the
// worker attempt.
type recordDataExchangeImportProvider struct {
	service   *RecordImportApplicationService
	principal principalmodel.Principal
	seen      map[string]int
}

func newRecordDataExchangeImportProvider(service *RecordImportApplicationService, principal principalmodel.Principal) *recordDataExchangeImportProvider {
	return &recordDataExchangeImportProvider{service: service, principal: principal, seen: map[string]int{}}
}

// legacyRecordDataExchangeImportProvider keeps the pre-extraction Runtime
// worker compiling while production composition moves to the SDK Binding.
// It remains only for legacy RecordBatchJob fallback compatibility; both paths
// now use the Data Exchange owner's fileengine implementation.
type legacyRecordDataExchangeImportProvider struct {
	sdk *recordDataExchangeImportProvider
}

func newLegacyRecordDataExchangeImportProvider(service *RecordImportApplicationService, principal principalmodel.Principal) *legacyRecordDataExchangeImportProvider {
	return &legacyRecordDataExchangeImportProvider{sdk: newRecordDataExchangeImportProvider(service, principal)}
}

func (p *legacyRecordDataExchangeImportProvider) ValidateImportBatch(ctx context.Context, batch dataexchangesdk.ImportBatch) (dataexchangesdk.ImportBatchResult, error) {
	return p.sdk.ValidateImportBatch(ctx, batch)
}

func (p *legacyRecordDataExchangeImportProvider) ApplyImportBatch(ctx context.Context, batch dataexchangesdk.ImportBatch) (dataexchangesdk.ImportBatchResult, error) {
	return p.sdk.ApplyImportBatch(ctx, batch)
}

func (p *recordDataExchangeImportProvider) ValidateImportBatch(ctx context.Context, batch dataexchangesdk.ImportBatch) (dataexchangesdk.ImportBatchResult, error) {
	if err := p.validateScope(batch.Scope); err != nil {
		return dataexchangesdk.ImportBatchResult{}, err
	}
	raw, err := recordDataExchangeCSV(batch)
	if err != nil {
		return dataexchangesdk.ImportBatchResult{}, err
	}
	preview, err := p.service.Preview(ctx, batch.ObjectKey, raw, p.principal)
	if err != nil {
		return dataexchangesdk.ImportBatchResult{}, err
	}
	object, err := p.service.dependencies.ObjectForAction(p.principal, batch.ObjectKey, "import")
	if err != nil {
		return dataexchangesdk.ImportBatchResult{}, err
	}
	accepted, rejected := 0, preview.InvalidRows
	for _, row := range preview.Rows {
		if !row.Valid {
			continue
		}
		duplicate := false
		for _, field := range recordvalidation.RecordDuplicateIdentityFields(object) {
			value := row.Data[field.Key]
			if recordvalidation.RecordIsEmptyValue(value) {
				continue
			}
			identity := field.Key + "=" + strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
			if _, found := p.seen[identity]; found {
				duplicate = true
			} else {
				p.seen[identity] = row.Row
			}
		}
		if duplicate {
			rejected++
		} else {
			accepted++
		}
	}
	return dataexchangesdk.ImportBatchResult{Accepted: accepted, Rejected: rejected, Receipt: batch.ChunkID + ":validated"}, nil
}

func (p *recordDataExchangeImportProvider) ApplyImportBatch(ctx context.Context, batch dataexchangesdk.ImportBatch) (dataexchangesdk.ImportBatchResult, error) {
	if err := p.validateScope(batch.Scope); err != nil {
		return dataexchangesdk.ImportBatchResult{}, err
	}
	raw, err := recordDataExchangeCSV(batch)
	if err != nil {
		return dataexchangesdk.ImportBatchResult{}, err
	}
	result, _, err := p.service.ApplyIdempotent(ctx, batch.ObjectKey, raw, "data-exchange:"+batch.ChunkID, p.principal)
	if err != nil {
		return dataexchangesdk.ImportBatchResult{}, err
	}
	return dataexchangesdk.ImportBatchResult{Accepted: result.Created, Rejected: result.Skipped, Receipt: batch.ChunkID + ":applied"}, nil
}

func (p *recordDataExchangeImportProvider) validateScope(scope dataexchangesdk.Scope) error {
	if p == nil || p.service == nil {
		return apperror.New(apperror.KindInternal, "backend.record_batch.import_provider_unavailable", nil, nil)
	}
	if strings.TrimSpace(scope.WorkspaceID) != strings.TrimSpace(p.principal.WorkspaceID) || strings.TrimSpace(scope.ActorID) != strings.TrimSpace(p.principal.UserID) {
		return apperror.New(apperror.KindForbidden, "backend.record_batch.import_scope_mismatch", nil, nil)
	}
	return nil
}

func recordDataExchangeCSV(batch dataexchangesdk.ImportBatch) ([]byte, error) {
	var output bytes.Buffer
	encoder := fileexchange.NewCSVEncoder(&output, recordImportMaxBytes)
	if err := encoder.Write(batch.Headers); err != nil {
		return nil, err
	}
	for _, row := range batch.Rows {
		if len(row.Values) != len(batch.Headers) {
			return nil, apperror.New(apperror.KindBadRequest, "backend.import.invalid_csv", nil, nil)
		}
		if err := encoder.Write(row.Values); err != nil {
			return nil, err
		}
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

var _ modulehost.ImportProvider = (*recordDataExchangeImportProvider)(nil)
var _ modulehost.ImportProvider = (*legacyRecordDataExchangeImportProvider)(nil)
