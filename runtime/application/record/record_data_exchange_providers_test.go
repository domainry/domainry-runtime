package record

import (
	"context"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-data-exchange-sdk/modulehost"
)

type externalDataExchangeProviderStub struct{}

func (externalDataExchangeProviderStub) ValidateImportBatch(context.Context, dataexchange.ImportBatch) (dataexchange.ImportBatchResult, error) {
	return dataexchange.ImportBatchResult{}, nil
}

func (externalDataExchangeProviderStub) ApplyImportBatch(context.Context, dataexchange.ImportBatch) (dataexchange.ImportBatchResult, error) {
	return dataexchange.ImportBatchResult{}, nil
}

func (externalDataExchangeProviderStub) ValidateImportArtifact(context.Context, dataexchange.ImportArtifact) (dataexchange.ImportArtifactResult, error) {
	return dataexchange.ImportArtifactResult{}, nil
}

func (externalDataExchangeProviderStub) ApplyImportArtifact(context.Context, dataexchange.ImportArtifact) (dataexchange.ImportArtifactResult, error) {
	return dataexchange.ImportArtifactResult{}, nil
}

func (externalDataExchangeProviderStub) ReadExportPage(context.Context, dataexchange.ExportPageRequest) (dataexchange.ExportPage, error) {
	return dataexchange.ExportPage{}, nil
}

func (externalDataExchangeProviderStub) BuildExportArtifact(context.Context, dataexchange.ExportArtifactRequest) (dataexchange.ExportArtifact, error) {
	return dataexchange.ExportArtifact{}, nil
}

func TestDataExchangeProvidersRegistersExternalArtifactProvider(t *testing.T) {
	providers := NewDataExchangeProviders(nil)
	external := externalDataExchangeProviderStub{}
	providers.RegisterImportProvider(" identity-portability ", external)
	providers.RegisterExportProvider(" identity-portability ", external)

	importProvider, ok := providers.ImportProvider("identity-portability")
	if !ok || importProvider == nil {
		t.Fatal("registered import provider is unavailable")
	}
	if _, ok := importProvider.(modulehost.ImportArtifactProvider); !ok {
		t.Fatalf("artifact import capability was erased: %T", importProvider)
	}
	exportProvider, ok := providers.ExportProvider("identity-portability")
	if !ok || exportProvider == nil {
		t.Fatal("registered export provider is unavailable")
	}
	if _, ok := exportProvider.(modulehost.ExportArtifactProvider); !ok {
		t.Fatalf("artifact export capability was erased: %T", exportProvider)
	}
}
