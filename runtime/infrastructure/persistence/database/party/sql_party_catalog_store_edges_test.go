package party

import (
	"database/sql/driver"
	"testing"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
)

func TestSQLPartyCatalogReadEdges(t *testing.T) {
	jobColumns := []string{"id", "code", "name", "family", "level", "description", "status"}
	positionColumns := []string{"id", "code", "name", "job_catalog_item_id", "organization_unit_id", "headcount", "effective_from", "effective_to", "status"}

	store, closeDB := scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{{
		columns: jobColumns,
		rows: [][]driver.Value{
			{"z", "Z", "Z", "", "", "", "active"},
			{"a", "A", "A", "", "", "", "active"},
		},
	}}})
	jobs, err := store.ListJobs(t.Context(), "workspace")
	closeDB()
	if err != nil || len(jobs) != 2 || jobs[0].ID != "a" {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}

	store, closeDB = scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{{
		columns: positionColumns,
		rows: [][]driver.Value{
			{"z", "Z", "Z", "", "", int64(1), "", "", "active"},
			{"a", "A", "A", "", "", int64(2), "", "", "active"},
		},
	}}})
	positions, err := store.ListPositions(t.Context(), "workspace")
	closeDB()
	if err != nil || len(positions) != 2 || positions[0].ID != "a" {
		t.Fatalf("positions=%#v err=%v", positions, err)
	}

	for _, test := range []struct {
		name    string
		columns []string
		call    func(*SQLPartyStore) error
	}{
		{name: "jobs", columns: jobColumns, call: func(store *SQLPartyStore) error { _, err := store.ListJobs(t.Context(), "workspace"); return err }},
		{name: "positions", columns: positionColumns, call: func(store *SQLPartyStore) error { _, err := store.ListPositions(t.Context(), "workspace"); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, state := range []*partySQLState{
				{queryFailAt: 1, failure: errPartySQL},
				{querySteps: []partySQLQueryStep{{columns: []string{"only"}, rows: [][]driver.Value{{"value"}}}}},
				{querySteps: []partySQLQueryStep{{columns: test.columns, nextErr: errPartySQL}}},
			} {
				store, closeDB := scriptedPartyStore(state)
				if err := test.call(store); err == nil {
					t.Fatal("catalog list failure ignored")
				}
				closeDB()
			}
		})
	}

	for _, test := range []struct {
		name string
		get  func(*SQLPartyStore) (bool, error)
	}{
		{name: "job", get: func(store *SQLPartyStore) (bool, error) {
			_, found, err := store.GetJob(t.Context(), "workspace", "item")
			return found, err
		}},
		{name: "position", get: func(store *SQLPartyStore) (bool, error) {
			_, found, err := store.GetPosition(t.Context(), "workspace", "item")
			return found, err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedPartyStore(&partySQLState{})
			found, err := test.get(store)
			closeDB()
			if err != nil || found {
				t.Fatalf("missing found=%v err=%v", found, err)
			}
			store, closeDB = scriptedPartyStore(&partySQLState{queryFailAt: 1, failure: errPartySQL})
			_, err = test.get(store)
			closeDB()
			if err == nil {
				t.Fatal("catalog get failure ignored")
			}
		})
	}
}

func TestSQLPartyCatalogUpsertFailureStages(t *testing.T) {
	job := partymodel.JobCatalogItem{ID: "job", Code: "ENG", Name: "Engineer", Status: partymodel.PartyStatusActive}
	position := partymodel.Position{ID: "position", Code: "P", Name: "Position", Headcount: 1, Status: partymodel.PartyStatusActive}
	for _, test := range []struct {
		name string
		call func(*SQLPartyStore) error
	}{
		{name: "job", call: func(store *SQLPartyStore) error { _, err := store.UpsertJob(t.Context(), "workspace", job); return err }},
		{name: "position", call: func(store *SQLPartyStore) error {
			_, err := store.UpsertPosition(t.Context(), "workspace", position)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedPartyStore(&partySQLState{})
			if err := test.call(store); err != nil {
				t.Fatalf("successful upsert: %v", err)
			}
			closeDB()
			for _, state := range []*partySQLState{
				{beginErr: errPartySQL},
				{execFailAt: 1, failure: errPartySQL},
				{execFailAt: 2, failure: errPartySQL},
				{commitErr: errPartySQL},
			} {
				store, closeDB = scriptedPartyStore(state)
				if err := test.call(store); err == nil {
					t.Fatal("catalog upsert failure ignored")
				}
				closeDB()
			}
		})
	}
}

func TestSQLPartyCatalogRejectsBlankWorkspace(t *testing.T) {
	for _, call := range []func(*SQLPartyStore) error{
		func(store *SQLPartyStore) error { _, err := store.ListJobs(t.Context(), ""); return err },
		func(store *SQLPartyStore) error { _, _, err := store.GetJob(t.Context(), "", "job"); return err },
		func(store *SQLPartyStore) error {
			_, err := store.UpsertJob(t.Context(), "", partymodel.JobCatalogItem{})
			return err
		},
		func(store *SQLPartyStore) error { _, err := store.ListPositions(t.Context(), ""); return err },
		func(store *SQLPartyStore) error {
			_, _, err := store.GetPosition(t.Context(), "", "position")
			return err
		},
		func(store *SQLPartyStore) error {
			_, err := store.UpsertPosition(t.Context(), "", partymodel.Position{})
			return err
		},
	} {
		store, closeDB := scriptedPartyStore(&partySQLState{})
		if err := call(store); err == nil {
			t.Fatal("blank workspace accepted")
		}
		closeDB()
	}
}
