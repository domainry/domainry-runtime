package changeplan

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
)

var _ interface {
	ListSeedProvenance(context.Context) ([]businessseedmodel.BusinessSeedProvenance, error)
} = BusinessEvidenceStore{}

func TestBusinessEvidenceStoreCancellation(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewBusinessEvidenceStore(store)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListSeedProvenance(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list error=%v", err)
	}
	if _, _, err := repository.GetSeedProvenance(cancelled, "seed"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled get error=%v", err)
	}
}

func TestBusinessEvidenceStoreUpsertReplaceAndList(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewBusinessEvidenceStore(store)
	value := businessseedmodel.BusinessSeedProvenance{SeedKey: "seed", ObjectKey: "customer", RecordID: "one", SourceKind: "template", SourceID: "source", TemplateID: "template", TemplateVersion: "v1", ContentHash: "hash", MaterializedAt: "now"}
	if err := repository.UpsertSeedProvenance(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	if missing, found, err := repository.GetSeedProvenance(t.Context(), "missing"); err != nil || found || missing.SeedKey != "" {
		t.Fatalf("missing=%#v found=%v err=%v", missing, found, err)
	}
	value.RecordID = "two"
	if err := repository.UpsertSeedProvenance(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	values, err := repository.ListSeedProvenance(t.Context())
	if err != nil || len(values) != 1 || values[0].RecordID != "two" {
		t.Fatalf("values=%#v err=%v", values, err)
	}
	if got, found, err := repository.GetSeedProvenance(t.Context(), "seed"); err != nil || !found || got.RecordID != "two" {
		t.Fatalf("got=%#v found=%v err=%v", got, found, err)
	}
	if options := transactionOptions(); options.Isolation != sql.LevelSerializable {
		t.Fatalf("isolation=%v", options.Isolation)
	}
}
