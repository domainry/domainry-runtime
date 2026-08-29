package dataexchange

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type importProviderProbe struct {
	validated [][]ImportRow
	applied   [][]ImportRow
	rejectAt  int
}

func (p *importProviderProbe) ValidateImportBatch(_ context.Context, batch ImportBatch) (ImportBatchResult, error) {
	p.validated = append(p.validated, batch.Rows)
	if p.rejectAt == len(p.validated) {
		return ImportBatchResult{Rejected: len(batch.Rows)}, nil
	}
	return ImportBatchResult{Accepted: len(batch.Rows)}, nil
}

func (p *importProviderProbe) ApplyImportBatch(_ context.Context, batch ImportBatch) (ImportBatchResult, error) {
	p.applied = append(p.applied, batch.Rows)
	return ImportBatchResult{Accepted: len(batch.Rows)}, nil
}

func TestProcessCSVImportValidatesThenAppliesBoundedBatches(t *testing.T) {
	provider := &importProviderProbe{}
	value := "name\nAda\nGrace\nLinus\n"
	result, err := ProcessCSVImport(t.Context(), provider, ImportEngineRequest{
		Batch: ImportBatch{JobID: "job", ObjectKey: "customer"}, BatchSize: 2,
		Limits: CSVDecodeLimits{MaxBytes: 128, MaxRows: 10, MaxColumns: 2},
		Open:   func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(value)), nil },
	})
	if err != nil || result.Validated != 3 || result.Applied != 3 || result.Batches != 2 || result.SHA256 == "" || len(provider.validated) != 2 || len(provider.applied) != 2 {
		t.Fatalf("result=%+v validated=%v applied=%v err=%v", result, provider.validated, provider.applied, err)
	}
}

func TestProcessCSVImportDoesNotApplyRejectedValidation(t *testing.T) {
	provider := &importProviderProbe{rejectAt: 2}
	result, err := ProcessCSVImport(t.Context(), provider, ImportEngineRequest{
		Batch: ImportBatch{JobID: "job"}, BatchSize: 1,
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("name\nAda\nGrace\n")), nil
		},
	})
	if err != nil || result.Rejected != 1 || result.Applied != 0 || len(provider.applied) != 0 {
		t.Fatalf("result=%+v applied=%v err=%v", result, provider.applied, err)
	}
}

func TestProcessCSVImportRejectsChangedSourceAndInvalidProviderResult(t *testing.T) {
	provider := &importProviderProbe{}
	opened := 0
	_, err := ProcessCSVImport(t.Context(), provider, ImportEngineRequest{
		Batch: ImportBatch{JobID: "job"}, BatchSize: 1,
		Open: func(context.Context) (io.ReadCloser, error) {
			opened++
			value := "name\nAda\n"
			if opened == 2 {
				value = "name\nGrace\n"
			}
			return io.NopCloser(strings.NewReader(value)), nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("err=%v", err)
	}

	bad := invalidImportProvider{}
	_, err = ProcessCSVImport(t.Context(), bad, ImportEngineRequest{BatchSize: 1, Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("name\nAda\n")), nil
	}})
	if err == nil {
		t.Fatal("invalid provider result was accepted")
	}
}

type invalidImportProvider struct{}

func (invalidImportProvider) ValidateImportBatch(context.Context, ImportBatch) (ImportBatchResult, error) {
	return ImportBatchResult{}, nil
}
func (invalidImportProvider) ApplyImportBatch(context.Context, ImportBatch) (ImportBatchResult, error) {
	return ImportBatchResult{}, errors.New("must not apply")
}
