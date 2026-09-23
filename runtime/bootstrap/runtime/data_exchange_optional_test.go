package runtime

import (
	"path/filepath"
	"strings"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

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
