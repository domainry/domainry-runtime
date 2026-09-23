package runtime

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type subjectLifecyclePersistenceDataExchangeBinding struct {
	dataexchange.Binding
	descriptor dataexchange.Descriptor
	bindErr    error
	bindCalls  int
}

func (b *subjectLifecyclePersistenceDataExchangeBinding) Descriptor() dataexchange.Descriptor {
	return b.descriptor
}

func (b *subjectLifecyclePersistenceDataExchangeBinding) BindSubjectLifecyclePersistence() error {
	b.bindCalls++
	return b.bindErr
}

type dataExchangeBindingWithoutSubjectLifecyclePersistence struct {
	dataexchange.Binding
}

func (dataExchangeBindingWithoutSubjectLifecyclePersistence) Descriptor() dataexchange.Descriptor {
	return dataexchange.Descriptor{Mode: dataexchange.DeploymentModeModule}
}

func TestMissingDataExchangeFactoryLeavesCapabilityAndStorageUninstalled(t *testing.T) {
	store, err := prepareRuntimeStore(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	binding, providers, err := openOptionalDataExchangeBinding(
		t.Context(),
		nil,
		dataexchange.ApplicationRef{ApplicationID: "test", RuntimeID: "test"},
		store,
		"records",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("open optional Data Exchange capability: %v", err)
	}
	if binding != nil || providers != nil {
		t.Fatalf("missing factory installed Data Exchange capability: binding=%v providers=%v", binding, providers)
	}

	rows, err := store.DB().QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	unselectedPrefixes := []string{"_data_exchange_", "_integration_", "_notification_", "_scheduler_", "_agent_", "_monitoring_"}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		for _, prefix := range unselectedPrefixes {
			if strings.HasPrefix(name, prefix) {
				t.Fatalf("fresh Runtime store materialized unselected module table %q", name)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestBindEmbeddedDataExchangeSubjectLifecyclePersistence(t *testing.T) {
	binding := &subjectLifecyclePersistenceDataExchangeBinding{
		descriptor: dataexchange.Descriptor{Mode: dataexchange.DeploymentModeModule},
	}
	if err := bindEmbeddedDataExchangeSubjectLifecyclePersistence(binding); err != nil {
		t.Fatalf("bind embedded Data Exchange subject lifecycle persistence: %v", err)
	}
	if binding.bindCalls != 1 {
		t.Fatalf("bind calls = %d, want 1", binding.bindCalls)
	}
}

func TestBindEmbeddedDataExchangeSubjectLifecyclePersistenceSkipsSaaS(t *testing.T) {
	binding := &subjectLifecyclePersistenceDataExchangeBinding{
		descriptor: dataexchange.Descriptor{Mode: dataexchange.DeploymentModeSaaS},
	}
	if err := bindEmbeddedDataExchangeSubjectLifecyclePersistence(binding); err != nil {
		t.Fatalf("skip SaaS Data Exchange subject lifecycle persistence: %v", err)
	}
	if binding.bindCalls != 0 {
		t.Fatalf("bind calls = %d, want 0", binding.bindCalls)
	}
}

func TestBindEmbeddedDataExchangeSubjectLifecyclePersistenceRequiresBinder(t *testing.T) {
	err := bindEmbeddedDataExchangeSubjectLifecyclePersistence(dataExchangeBindingWithoutSubjectLifecyclePersistence{})
	if err == nil || !strings.Contains(err.Error(), "no shared subject lifecycle persistence binder") {
		t.Fatalf("missing binder error = %v", err)
	}
}

func TestBindEmbeddedDataExchangeSubjectLifecyclePersistencePropagatesFailure(t *testing.T) {
	wantErr := errors.New("shared tables unavailable")
	binding := &subjectLifecyclePersistenceDataExchangeBinding{
		descriptor: dataexchange.Descriptor{Mode: dataexchange.DeploymentModeModule},
		bindErr:    wantErr,
	}
	err := bindEmbeddedDataExchangeSubjectLifecyclePersistence(binding)
	if !errors.Is(err, wantErr) {
		t.Fatalf("bind error = %v, want %v", err, wantErr)
	}
}
