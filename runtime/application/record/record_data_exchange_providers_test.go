package record

import (
	"context"
	"errors"
	"testing"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-data-exchange-sdk/modulehost"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type externalDataExchangeProviderStub struct{}

func (externalDataExchangeProviderStub) ValidateImportBatch(context.Context, dataexchange.ImportBatch) (dataexchange.ImportBatchResult, error) {
	return dataexchange.ImportBatchResult{}, nil
}

func TestRecordProviderProjectsLegacyJobShapeForSharedHTTPRoute(t *testing.T) {
	providers := NewDataExchangeProviders(nil)
	provider, ok := providers.ImportProvider("records")
	projector, projected := provider.(modulehost.JobProjector)
	if !ok || !projected {
		t.Fatalf("Record provider projection is unavailable: %T", provider)
	}
	now := time.Now().UTC()
	value, err := projector.ProjectDataExchangeJob(t.Context(), dataexchange.Job{
		ID: "job-1", Provider: "records", Operation: "import", WorkspaceID: "workspace", ActorID: "actor", Status: "running", CreatedAt: now, UpdatedAt: now,
	}, dataexchange.Scope{WorkspaceID: "workspace", ActorID: "actor"})
	job, typed := value.(recordmodel.RecordBatchJob)
	if err != nil || !typed || job.ID != "job-1" || job.Kind != "import" || job.Status != "running" {
		t.Fatalf("projection=%#v err=%v", value, err)
	}
	if _, err = projector.ProjectDataExchangeJob(t.Context(), dataexchange.Job{Provider: "reports"}, dataexchange.Scope{}); !errors.Is(err, dataexchange.ErrJobNotFound) {
		t.Fatalf("cross-provider projection error=%v", err)
	}
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
	if err := providers.RegisterImportProvider(" identity-portability ", external); err != nil {
		t.Fatal(err)
	}
	if err := providers.RegisterExportProvider(" identity-portability ", external); err != nil {
		t.Fatal(err)
	}

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

func TestDataExchangeProviderRegistryRejectsInvalidDuplicateReservedAndLateRegistration(t *testing.T) {
	external := externalDataExchangeProviderStub{}
	var missing *DataExchangeProviders
	if err := missing.RegisterImportProvider("identity-portability", external); !errors.Is(err, ErrDataExchangeProviderRegistryRequired) {
		t.Fatalf("nil registry error=%v", err)
	}
	providers := NewDataExchangeProviders(nil)
	for _, test := range []struct {
		name string
		key  string
		in   modulehost.ImportProvider
		want error
	}{
		{name: "blank", key: " ", in: external, want: ErrDataExchangeProviderKeyInvalid},
		{name: "unstable", key: "Upper Case", in: external, want: ErrDataExchangeProviderKeyInvalid},
		{name: "reserved", key: "records", in: external, want: ErrDataExchangeProviderKeyReserved},
		{name: "nil", key: "identity-portability", want: ErrDataExchangeProviderRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := providers.RegisterImportProvider(test.key, test.in); !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
	if err := providers.RegisterImportProvider("identity-portability", external); err != nil {
		t.Fatal(err)
	}
	if err := providers.RegisterImportProvider("identity-portability", external); !errors.Is(err, ErrDataExchangeProviderDuplicate) {
		t.Fatalf("duplicate import error=%v", err)
	}
	// Import and export are distinct owner capabilities and may intentionally
	// share one provider identity.
	if err := providers.RegisterExportProvider("identity-portability", external); err != nil {
		t.Fatal(err)
	}
	if err := providers.RegisterExportProvider("identity-portability", external); !errors.Is(err, ErrDataExchangeProviderDuplicate) {
		t.Fatalf("duplicate export error=%v", err)
	}
	providers.Freeze()
	if !providers.Frozen() {
		t.Fatal("provider registry did not freeze")
	}
	if err := providers.RegisterImportProvider("late", external); !errors.Is(err, ErrDataExchangeProviderRegistryFrozen) {
		t.Fatalf("late import error=%v", err)
	}
	if err := providers.RegisterExportProvider("late", external); !errors.Is(err, ErrDataExchangeProviderRegistryFrozen) {
		t.Fatalf("late export error=%v", err)
	}
	if _, ok := providers.ImportProvider("identity-portability"); !ok {
		t.Fatal("freeze removed existing import provider")
	}
	if _, ok := providers.ExportProvider("identity-portability"); !ok {
		t.Fatal("freeze removed existing export provider")
	}
}

func TestRecordImportProviderResetsAndReleasesAttemptState(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true, Unique: true}}}
	importer := NewRecordImportApplicationService(RecordImportDependencies{
		Repository: &importRepositoryProbe{existing: map[string]bool{}},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	})
	providers := NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal {
		return recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "actor"}})
	})
	providers.Bind(importer, nil)
	provider, ok := providers.ImportProvider("records")
	if !ok {
		t.Fatal("Record import provider is unavailable")
	}
	batch := dataexchange.ImportBatch{
		Scope: dataexchange.Scope{WorkspaceID: "workspace", ActorID: "actor"}, ObjectKey: object.Key, JobID: "job", ChunkID: "job:1",
		Attempt: 1, Headers: []string{"name"}, Rows: []dataexchange.ImportRow{{Number: 2, Values: []string{"Acme"}}},
	}
	first, err := provider.ValidateImportBatch(t.Context(), batch)
	if err != nil || first.Accepted != 1 || first.Rejected != 0 {
		t.Fatalf("first attempt result=%+v err=%v", first, err)
	}
	batch.Attempt, batch.Final = 2, true
	second, err := provider.ValidateImportBatch(t.Context(), batch)
	if err != nil || second.Accepted != 1 || second.Rejected != 0 {
		t.Fatalf("retry reused stale duplicate state: result=%+v err=%v", second, err)
	}
	providers.mu.Lock()
	defer providers.mu.Unlock()
	if len(providers.imports) != 0 {
		t.Fatalf("completed validation retained %d attempt states", len(providers.imports))
	}
}
