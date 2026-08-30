package runtime

import (
	"context"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	dataexchangemodulehost "github.com/domainry/domainry-data-exchange-sdk/modulehost"
)

type runtimeIdentityDataExchangeProvider struct{}

func (runtimeIdentityDataExchangeProvider) ValidateImportBatch(context.Context, dataexchange.ImportBatch) (dataexchange.ImportBatchResult, error) {
	return dataexchange.ImportBatchResult{}, nil
}

func (runtimeIdentityDataExchangeProvider) ApplyImportBatch(context.Context, dataexchange.ImportBatch) (dataexchange.ImportBatchResult, error) {
	return dataexchange.ImportBatchResult{}, nil
}

func (runtimeIdentityDataExchangeProvider) ReadExportPage(context.Context, dataexchange.ExportPageRequest) (dataexchange.ExportPage, error) {
	return dataexchange.ExportPage{}, nil
}

type runtimeIdentityDataExchangeBinding struct {
	runtimeIdentityBindingStub
	provider runtimeIdentityDataExchangeProvider
}

func (binding runtimeIdentityDataExchangeBinding) IdentityDataExchangeProviders() (string, dataexchangemodulehost.ImportProvider, dataexchangemodulehost.ExportProvider) {
	return "identity-portability", binding.provider, binding.provider
}

func TestIdentityDataExchangeProvidersAreExtractedFromOptionalBinding(t *testing.T) {
	if key, importer, exporter := identityDataExchangeProviders(runtimeIdentityBindingStub{}); key != "" || importer != nil || exporter != nil {
		t.Fatalf("base Identity binding unexpectedly exposed providers: key=%q import=%T export=%T", key, importer, exporter)
	}
	binding := runtimeIdentityDataExchangeBinding{}
	key, importer, exporter := identityDataExchangeProviders(binding)
	if key != "identity-portability" || importer == nil || exporter == nil {
		t.Fatalf("Identity providers key=%q import=%T export=%T", key, importer, exporter)
	}
}
