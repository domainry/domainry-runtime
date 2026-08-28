package party

import (
	"database/sql/driver"
	"testing"
	"time"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
)

func TestSQLPartyOrganizationExtensionReadEdges(t *testing.T) {
	extensionColumns := []string{"id", "kind", "code", "name", "claim_value", "parent_id", "organization_unit_id", "status"}
	store, closeDB := scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{{
		columns: extensionColumns,
		rows: [][]driver.Value{
			{"z", "team", "Z", "Z", "", "", "", "active"},
			{"a", "team", "A", "A", "", "", "", "active"},
		},
	}}})
	values, err := store.ListOrganizationExtensions(t.Context(), "workspace")
	closeDB()
	if err != nil || len(values) != 2 || values[0].ID != "a" {
		t.Fatalf("extensions=%#v err=%v", values, err)
	}

	for _, state := range []*partySQLState{
		{queryFailAt: 1, failure: errPartySQL},
		{querySteps: []partySQLQueryStep{{columns: []string{"only"}, rows: [][]driver.Value{{"value"}}}}},
		{querySteps: []partySQLQueryStep{{columns: extensionColumns, nextErr: errPartySQL}}},
	} {
		store, closeDB = scriptedPartyStore(state)
		if _, err := store.ListOrganizationExtensions(t.Context(), "workspace"); err == nil {
			t.Fatal("extension list failure ignored")
		}
		closeDB()
	}
	store, closeDB = scriptedPartyStore(&partySQLState{})
	if _, found, err := store.GetOrganizationExtension(t.Context(), "workspace", "missing"); err != nil || found {
		t.Fatalf("missing extension found=%v err=%v", found, err)
	}
	closeDB()
	store, closeDB = scriptedPartyStore(&partySQLState{queryFailAt: 1, failure: errPartySQL})
	if _, _, err := store.GetOrganizationExtension(t.Context(), "workspace", "extension"); err == nil {
		t.Fatal("extension get failure ignored")
	}
	closeDB()

	for _, call := range []func(*SQLPartyStore) error{
		func(store *SQLPartyStore) error {
			_, err := store.ListOrganizationExtensions(t.Context(), "")
			return err
		},
		func(store *SQLPartyStore) error {
			_, _, err := store.GetOrganizationExtension(t.Context(), "", "extension")
			return err
		},
	} {
		store, closeDB = scriptedPartyStore(&partySQLState{})
		if err := call(store); err == nil {
			t.Fatal("blank workspace accepted")
		}
		closeDB()
	}
}

func TestSQLPartyOrganizationExtensionUpsertFailureStages(t *testing.T) {
	value := partymodel.OrganizationExtension{ID: "extension", Kind: "team", Code: "T", Name: "Team", Status: partymodel.PartyStatusActive}
	store, closeDB := scriptedPartyStore(&partySQLState{})
	if got, err := store.UpsertOrganizationExtension(t.Context(), "workspace", value); err != nil || got.ID != value.ID {
		t.Fatalf("upsert=%#v err=%v", got, err)
	}
	closeDB()

	for _, state := range []*partySQLState{
		{beginErr: errPartySQL},
		{execFailAt: 1, failure: errPartySQL},
		{execFailAt: 2, failure: errPartySQL},
		{commitErr: errPartySQL},
	} {
		store, closeDB = scriptedPartyStore(state)
		if _, err := store.UpsertOrganizationExtension(t.Context(), "workspace", value); err == nil {
			t.Fatal("extension upsert failure ignored")
		}
		closeDB()
	}
	store, closeDB = scriptedPartyStore(&partySQLState{})
	if _, err := store.UpsertOrganizationExtension(t.Context(), "", value); err == nil {
		t.Fatal("blank workspace extension upsert accepted")
	}
	closeDB()
}

func TestSQLPartyOrganizationMembershipReadAndUpsertEdges(t *testing.T) {
	columns := []string{"id", "extension_id", "workforce_profile_id", "effective_from", "effective_to", "status"}
	for _, profileID := range []string{"", " workforce "} {
		store, closeDB := scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{{
			columns: columns,
			rows: [][]driver.Value{
				{"z", "extension", "workforce", "", "", "active"},
				{"a", "extension", "workforce", "", "", "active"},
			},
		}}})
		values, err := store.ListOrganizationExtensionMemberships(t.Context(), "workspace", profileID)
		closeDB()
		if err != nil || len(values) != 2 || values[0].ID != "a" {
			t.Fatalf("profile=%q memberships=%#v err=%v", profileID, values, err)
		}
	}
	for _, state := range []*partySQLState{
		{queryFailAt: 1, failure: errPartySQL},
		{querySteps: []partySQLQueryStep{{columns: []string{"only"}, rows: [][]driver.Value{{"value"}}}}},
		{querySteps: []partySQLQueryStep{{columns: columns, nextErr: errPartySQL}}},
	} {
		store, closeDB := scriptedPartyStore(state)
		if _, err := store.ListOrganizationExtensionMemberships(t.Context(), "workspace", "workforce"); err == nil {
			t.Fatal("membership list failure ignored")
		}
		closeDB()
	}

	value := partymodel.OrganizationExtensionMembership{ID: "membership", ExtensionID: "extension", WorkforceProfileID: "workforce", Status: partymodel.PartyStatusActive}
	store, closeDB := scriptedPartyStore(&partySQLState{})
	if got, err := store.UpsertOrganizationExtensionMembership(t.Context(), "workspace", value); err != nil || got.ID != value.ID {
		t.Fatalf("membership upsert=%#v err=%v", got, err)
	}
	closeDB()
	for _, state := range []*partySQLState{
		{beginErr: errPartySQL},
		{execFailAt: 1, failure: errPartySQL},
		{execFailAt: 2, failure: errPartySQL},
		{commitErr: errPartySQL},
	} {
		store, closeDB = scriptedPartyStore(state)
		if _, err := store.UpsertOrganizationExtensionMembership(t.Context(), "workspace", value); err == nil {
			t.Fatal("membership upsert failure ignored")
		}
		closeDB()
	}
	for _, call := range []func(*SQLPartyStore) error{
		func(store *SQLPartyStore) error {
			_, err := store.ListOrganizationExtensionMemberships(t.Context(), "", "")
			return err
		},
		func(store *SQLPartyStore) error {
			_, err := store.UpsertOrganizationExtensionMembership(t.Context(), "", value)
			return err
		},
	} {
		store, closeDB = scriptedPartyStore(&partySQLState{})
		if err := call(store); err == nil {
			t.Fatal("blank workspace membership operation accepted")
		}
		closeDB()
	}
}

func TestSQLPartyOrganizationScopeResolutionEdges(t *testing.T) {
	columns := []string{"kind", "claim_value", "extension_id", "effective_from", "effective_to", "membership_status", "extension_status"}
	store, closeDB := scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{
		{
			columns: columns,
			rows: [][]driver.Value{
				{"team", "skip", "inactive", "", "", "inactive", "active"},
				{"team", "skip", "future", "2999-01-01T00:00:00Z", "", "active", "active"},
				{"team", "skip", "expired", "", "2000-01-01T00:00:00Z", "active", "active"},
				{"team", "", "known", "invalid", "invalid", "active", "active"},
				{"unknown", "", "unknown-kind", "", "", "active", "active"},
				{"team", "", "inactive-extension", "", "", "active", "inactive"},
			},
		},
	}})
	facts, err := store.ResolveIdentityOrganizationScopes(t.Context(), "workspace", []string{"workforce"})
	closeDB()
	if err != nil || len(facts.TeamIDs) != 1 || facts.TeamIDs[0] != "known" {
		t.Fatalf("facts=%#v err=%v", facts, err)
	}

	store, closeDB = scriptedPartyStore(&partySQLState{queryFailAt: 1, failure: errPartySQL})
	if _, err := store.ResolveIdentityOrganizationScopes(t.Context(), "workspace", []string{"workforce"}); err == nil {
		t.Fatal("membership resolution failure ignored")
	}
	closeDB()
	store, closeDB = scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{{columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}}}})
	if _, err := store.ResolveIdentityOrganizationScopes(t.Context(), "workspace", []string{"workforce"}); err == nil {
		t.Fatal("organization scope scan failure ignored")
	}
	closeDB()
	store, closeDB = scriptedPartyStore(&partySQLState{querySteps: []partySQLQueryStep{{columns: columns, nextErr: errPartySQL}}})
	if _, err := store.ResolveIdentityOrganizationScopes(t.Context(), "workspace", []string{"workforce"}); err == nil {
		t.Fatal("organization scope iteration failure ignored")
	}
	closeDB()
	store, closeDB = scriptedPartyStore(&partySQLState{})
	if facts, err := store.ResolveIdentityOrganizationScopes(t.Context(), "workspace", nil); err != nil || len(facts.TeamIDs) != 0 {
		t.Fatalf("empty profiles facts=%#v err=%v", facts, err)
	}
	closeDB()
}

func TestOrganizationMembershipEffectiveEdges(t *testing.T) {
	now := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		value partymodel.OrganizationExtensionMembership
		want  bool
	}{
		{value: partymodel.OrganizationExtensionMembership{}, want: true},
		{value: partymodel.OrganizationExtensionMembership{EffectiveFrom: "invalid", EffectiveTo: "invalid"}, want: true},
		{value: partymodel.OrganizationExtensionMembership{EffectiveFrom: "2026-01-03T00:00:00Z"}, want: false},
		{value: partymodel.OrganizationExtensionMembership{EffectiveFrom: "2026-01-01T00:00:00Z"}, want: true},
		{value: partymodel.OrganizationExtensionMembership{EffectiveTo: "2026-01-02T00:00:00Z"}, want: false},
		{value: partymodel.OrganizationExtensionMembership{EffectiveTo: "2026-01-03T00:00:00Z"}, want: true},
	} {
		if got := organizationMembershipEffective(test.value, now); got != test.want {
			t.Fatalf("effective %#v = %v, want %v", test.value, got, test.want)
		}
	}
}
