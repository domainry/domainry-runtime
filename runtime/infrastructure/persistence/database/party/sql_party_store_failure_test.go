package party

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
)

var errPartySQL = errors.New("party SQL failure")

func fullPartyAggregate() partymodel.Aggregate {
	return partymodel.Aggregate{
		Party:                    partymodel.Party{ID: "party", Kind: partymodel.PartyKindPerson},
		Person:                   &partymodel.Person{PartyID: "party"},
		ContactPoints:            []partymodel.ContactPoint{{ID: "contact"}},
		Addresses:                []partymodel.Address{{ID: "address"}},
		Identifiers:              []partymodel.Identifier{{ID: "identifier"}},
		CommunicationPreferences: []partymodel.CommunicationPreference{{ID: "preference"}},
		Consents:                 []partymodel.Consent{{ID: "consent"}},
		PrivacyPreferences:       []partymodel.PrivacyPreference{{ID: "privacy"}},
		MarketingSubscriptions:   []partymodel.MarketingSubscription{{ID: "subscription"}},
	}
}

func TestSQLPartyStoreDialectEdges(t *testing.T) {
	mysql := NewSQLPartyStore(nil, " mysql ", " ignored ")
	if mysql.identifier("id") != "`id`" || mysql.placeholder(1) != "?" || mysql.table("parties") != "`parties`" {
		t.Fatal("mysql dialect mismatch")
	}
	postgres := NewSQLPartyStore(nil, "postgres", " tenant ")
	if postgres.identifier("id") != `"id"` || postgres.placeholder(2) != "$2" ||
		postgres.table("parties") != `"tenant"."parties"` || postgres.placeholders(2) != "$1, $2" ||
		postgres.columns("id", "kind") != `"id", "kind"` {
		t.Fatal("postgres dialect mismatch")
	}
	postgres.schema = ""
	if postgres.table("parties") != `"parties"` {
		t.Fatal("empty postgres schema mismatch")
	}
}

func TestSQLPartyStoreListAndGetFailures(t *testing.T) {
	for _, call := range []func(*SQLPartyStore) error{
		func(store *SQLPartyStore) error { _, err := store.List(t.Context(), "workspace"); return err },
		func(store *SQLPartyStore) error {
			_, _, err := store.Get(t.Context(), "workspace", "party")
			return err
		},
	} {
		store, closeDB := scriptedPartyStore(&partySQLState{queryFailAt: 1, failure: errPartySQL})
		if err := call(store); err == nil {
			t.Fatal("query failure ignored")
		}
		closeDB()
		store, closeDB = scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{{
			columns: []string{"only"}, rows: [][]driver.Value{{"value"}},
		}}})
		if err := call(store); err == nil {
			t.Fatal("scan failure ignored")
		}
		closeDB()
	}
	store, closeDB := scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{{
		columns: []string{"id", "kind", "display_name", "status", "version", "created_at", "updated_at"},
		nextErr: errPartySQL,
	}}})
	if _, err := store.List(t.Context(), "workspace"); err == nil {
		t.Fatal("list iteration failure ignored")
	}
	closeDB()
	store, closeDB = scriptedPartyStore(&partySQLState{})
	if _, found, err := store.Get(t.Context(), "workspace", "missing"); err != nil || found {
		t.Fatalf("missing party result: found=%v err=%v", found, err)
	}
	closeDB()
	partyRow := []driver.Value{"party", partymodel.PartyKindPerson, "Party", "active", int64(1), "created", "updated"}
	for _, call := range []func(*SQLPartyStore) error{
		func(store *SQLPartyStore) error { _, err := store.List(t.Context(), "workspace"); return err },
		func(store *SQLPartyStore) error {
			_, _, err := store.Get(t.Context(), "workspace", "party")
			return err
		},
	} {
		store, closeDB = scriptedPartyStore(&partySQLState{
			queryFailAt: 2, failure: errPartySQL,
			querySteps: []partySQLQueryStep{{
				columns: []string{"id", "kind", "display_name", "status", "version", "created_at", "updated_at"},
				rows:    [][]driver.Value{partyRow},
			}},
		})
		if err := call(store); err == nil {
			t.Fatal("detail load failure ignored")
		}
		closeDB()
	}
}

func TestSQLPartyStoreUpsertFailureStages(t *testing.T) {
	value := fullPartyAggregate()
	store, closeDB := scriptedPartyStore(&partySQLState{beginErr: errPartySQL})
	if _, err := store.Upsert(t.Context(), "workspace", value); err == nil {
		t.Fatal("begin failure ignored")
	}
	closeDB()
	for failAt := 1; failAt <= 19; failAt++ {
		store, closeDB = scriptedPartyStore(&partySQLState{execFailAt: failAt, failure: errPartySQL})
		if _, err := store.Upsert(t.Context(), "workspace", value); err == nil {
			t.Fatalf("exec failure %d ignored", failAt)
		}
		closeDB()
	}
	store, closeDB = scriptedPartyStore(&partySQLState{commitErr: errPartySQL})
	if _, err := store.Upsert(t.Context(), "workspace", value); err == nil {
		t.Fatal("commit failure ignored")
	}
	closeDB()

	organization := value
	organization.Party.Kind = partymodel.PartyKindOrganization
	organization.Person = nil
	organization.Organization = &partymodel.Organization{PartyID: "party"}
	store, closeDB = scriptedPartyStore(&partySQLState{})
	if _, err := store.Upsert(t.Context(), "workspace", organization); err != nil {
		t.Fatalf("organization upsert: %v", err)
	}
	closeDB()
}

func TestSQLPartyStoreDetailAndChildrenFailureStages(t *testing.T) {
	for _, value := range []*partymodel.Aggregate{
		{Party: partymodel.Party{ID: "party", Kind: partymodel.PartyKindPerson}},
		{Party: partymodel.Party{ID: "party", Kind: partymodel.PartyKindOrganization}},
	} {
		store, closeDB := scriptedPartyStore(&partySQLState{queryFailAt: 1, failure: errPartySQL})
		if err := store.loadDetail(t.Context(), "workspace", value); err == nil {
			t.Fatal("detail query failure ignored")
		}
		closeDB()
		store, closeDB = scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{{
			columns: []string{"only"}, rows: [][]driver.Value{{"value"}},
		}}})
		if err := store.loadDetail(t.Context(), "workspace", value); err == nil {
			t.Fatal("detail scan failure ignored")
		}
		closeDB()
	}

	for stage := 1; stage <= 7; stage++ {
		store, closeDB := scriptedPartyStore(&partySQLState{queryFailAt: stage, failure: errPartySQL})
		if err := store.loadChildren(t.Context(), "workspace", &partymodel.Aggregate{Party: partymodel.Party{ID: "party"}}); err == nil {
			t.Fatalf("child query failure %d ignored", stage)
		}
		closeDB()

		steps := make([]partySQLQueryStep, stage)
		steps[stage-1] = partySQLQueryStep{columns: []string{"only"}, rows: [][]driver.Value{{"value"}}}
		store, closeDB = scriptedPartyStore(&partySQLState{querySteps: steps})
		if err := store.loadChildren(t.Context(), "workspace", &partymodel.Aggregate{Party: partymodel.Party{ID: "party"}}); err == nil {
			t.Fatalf("child scan failure %d ignored", stage)
		}
		closeDB()

		steps = make([]partySQLQueryStep, stage)
		steps[stage-1] = partySQLQueryStep{columns: []string{"id"}, nextErr: errPartySQL}
		store, closeDB = scriptedPartyStore(&partySQLState{querySteps: steps})
		if err := store.loadChildren(t.Context(), "workspace", &partymodel.Aggregate{Party: partymodel.Party{ID: "party"}}); err == nil {
			t.Fatalf("child iteration failure %d ignored", stage)
		}
		closeDB()
	}
}

func TestSQLPartyStoreLoadsAndSortsEveryChildKind(t *testing.T) {
	store, closeDB := scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{
		{
			columns: []string{"id", "party_id", "type", "value", "label", "is_primary", "verified_at", "status"},
			rows: [][]driver.Value{
				{"z", "party", "email", "z@example.com", "", false, "", "active"},
				{"a", "party", "email", "a@example.com", "", true, "", "active"},
			},
		},
		{
			columns: []string{"id", "party_id", "type", "line1", "line2", "locality", "region", "postal_code", "country", "is_primary", "status"},
			rows: [][]driver.Value{
				{"z", "party", "home", "z", "", "", "", "", "", false, "active"},
				{"a", "party", "home", "a", "", "", "", "", "", true, "active"},
			},
		},
		{
			columns: []string{"id", "party_id", "type", "value", "issuer", "status"},
			rows: [][]driver.Value{
				{"z", "party", "external", "z", "", "active"},
				{"a", "party", "external", "a", "", "active"},
			},
		},
		{
			columns: []string{"id", "party_id", "channel", "allowed", "preferred", "locale"},
			rows: [][]driver.Value{
				{"z", "party", "email", true, false, "en"},
				{"a", "party", "email", true, true, "zh"},
			},
		},
		{
			columns: []string{"id", "party_id", "purpose", "status", "legal_basis", "source", "captured_at", "expires_at", "policy_version"},
			rows: [][]driver.Value{
				{"z", "party", "marketing", "granted", "", "web", "", "", ""},
				{"a", "party", "marketing", "granted", "", "web", "", "", ""},
			},
		},
		{
			columns: []string{"id", "party_id", "preference_key", "value", "updated_at"},
			rows: [][]driver.Value{
				{"z", "party", "tracking", "off", ""},
				{"a", "party", "tracking", "off", ""},
			},
		},
		{
			columns: []string{"id", "party_id", "channel", "topic", "status", "contact_point_id", "source", "subscribed_at", "unsubscribed_at"},
			rows: [][]driver.Value{
				{"z", "party", "email", "news", "active", "", "web", "", ""},
				{"a", "party", "email", "news", "active", "", "web", "", ""},
			},
		},
	}})
	defer closeDB()
	value := &partymodel.Aggregate{Party: partymodel.Party{ID: "party"}}
	if err := store.loadChildren(t.Context(), "workspace", value); err != nil {
		t.Fatal(err)
	}
	if value.ContactPoints[0].ID != "a" || value.Addresses[0].ID != "a" ||
		value.Identifiers[0].ID != "a" || value.CommunicationPreferences[0].ID != "a" ||
		value.Consents[0].ID != "a" || value.PrivacyPreferences[0].ID != "a" ||
		value.MarketingSubscriptions[0].ID != "a" {
		t.Fatalf("children were not sorted: %#v", value)
	}
}

func TestClosePartyRowsFailures(t *testing.T) {
	store, closeDB := scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{{
		columns: []string{"id"}, nextErr: errPartySQL,
	}}})
	rows, err := store.db.QueryContext(t.Context(), "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() || closePartyRows(rows, "test") == nil {
		t.Fatal("iteration error ignored")
	}
	closeDB()

	store, closeDB = scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{{
		columns: []string{"id"}, closeErr: errPartySQL,
	}}})
	rows, err = store.db.QueryContext(t.Context(), "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	err = closePartyRows(rows, "test")
	if err == nil || !strings.Contains(err.Error(), "party test") {
		t.Fatalf("close error = %v", err)
	}
	closeDB()
}
