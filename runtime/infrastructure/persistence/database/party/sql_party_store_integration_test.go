package party_test

import (
	"path/filepath"
	"testing"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	partyservice "github.com/domainry/domainry-runtime/runtime/domain/party/service"
	runtimedatabase "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	partypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/party"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestSQLPartyStorePersistsKindsAndIsolatesWorkspaces(t *testing.T) {
	runtimeStore, err := runtimedatabase.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	service := partyservice.NewPartyDomainService(partypersistence.NewSQLPartyStore(runtimeStore.DB(), "sqlite"))
	person, err := service.Upsert(t.Context(), "workspace-a", partymodel.Aggregate{
		Party:                    partymodel.Party{ID: "party-person", Kind: partymodel.PartyKindPerson, DisplayName: "Ada Lovelace"},
		Person:                   &partymodel.Person{GivenName: "Ada", FamilyName: "Lovelace", BirthDate: "1815-12-10"},
		ContactPoints:            []partymodel.ContactPoint{{ID: "email", Type: "email", Value: "ada@example.com", Primary: true, VerifiedAt: "2026-07-25T00:00:00Z"}},
		Addresses:                []partymodel.Address{{ID: "address", Type: "home", Line1: "1 Engine Way", Country: "GB", Primary: true}},
		Identifiers:              []partymodel.Identifier{{ID: "identifier", Type: "customer_no", Value: "C-1", Issuer: "CRM"}},
		CommunicationPreferences: []partymodel.CommunicationPreference{{ID: "preference", Channel: "email", Allowed: true, Preferred: true, Locale: "en-GB"}},
		Consents: []partymodel.Consent{{
			ID: "consent", Purpose: "marketing", Status: "granted", LegalBasis: "consent",
			Source: "web_form", CapturedAt: "2026-07-25T00:00:00Z", PolicyVersion: "v1",
		}},
		PrivacyPreferences: []partymodel.PrivacyPreference{{
			ID: "privacy", Key: "profiling", Value: "denied", UpdatedAt: "2026-07-25T00:00:00Z",
		}},
		MarketingSubscriptions: []partymodel.MarketingSubscription{{
			ID: "subscription", Channel: "email", Topic: "newsletter", Status: "subscribed", ContactPointID: "email",
			Source: "self_service", SubscribedAt: "2026-07-25T00:00:00Z",
		}},
	})
	if err != nil || person.Person == nil {
		t.Fatalf("person=%#v err=%v", person, err)
	}
	organization, err := service.Upsert(t.Context(), "workspace-a", partymodel.Aggregate{
		Party: partymodel.Party{ID: "party-organization", Kind: partymodel.PartyKindOrganization, DisplayName: "Analytical Engines"},
		Organization: &partymodel.Organization{
			LegalName: "Analytical Engines Ltd", RegistrationNumber: "REG-1",
		},
	})
	if err != nil || organization.Organization == nil {
		t.Fatalf("organization=%#v err=%v", organization, err)
	}
	if _, err := service.Upsert(t.Context(), "workspace-b", partymodel.Aggregate{
		Party:  partymodel.Party{ID: "party-person", Kind: partymodel.PartyKindPerson, DisplayName: "Other Ada"},
		Person: &partymodel.Person{GivenName: "Other"},
	}); err != nil {
		t.Fatal(err)
	}
	values, err := service.List(t.Context(), "workspace-a")
	if err != nil || len(values) != 2 || values[0].Party.ID != "party-organization" || values[1].Party.ID != "party-person" {
		t.Fatalf("values=%#v err=%v", values, err)
	}
	loaded, found, err := service.Get(t.Context(), "workspace-a", "party-person")
	if err != nil || !found || loaded.Person == nil || loaded.Person.GivenName != "Ada" || loaded.Organization != nil ||
		len(loaded.ContactPoints) != 1 || loaded.ContactPoints[0].Value != "ada@example.com" ||
		len(loaded.Addresses) != 1 || loaded.Addresses[0].Country != "GB" ||
		len(loaded.Identifiers) != 1 || loaded.Identifiers[0].Value != "C-1" ||
		len(loaded.CommunicationPreferences) != 1 || !loaded.CommunicationPreferences[0].Preferred ||
		len(loaded.Consents) != 1 || loaded.Consents[0].PolicyVersion != "v1" ||
		len(loaded.PrivacyPreferences) != 1 || loaded.PrivacyPreferences[0].Value != "denied" ||
		len(loaded.MarketingSubscriptions) != 1 || loaded.MarketingSubscriptions[0].ContactPointID != "email" {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}
	other, found, err := service.Get(t.Context(), "workspace-b", "party-person")
	if err != nil || !found || other.Party.DisplayName != "Other Ada" {
		t.Fatalf("other=%#v found=%v err=%v", other, found, err)
	}
	if _, found, err := service.Get(t.Context(), "workspace-b", "party-organization"); err != nil || found {
		t.Fatalf("cross-workspace found=%v err=%v", found, err)
	}
}

func TestSQLPartyStoreCanReplaceSubtypeAtomically(t *testing.T) {
	runtimeStore, err := runtimedatabase.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := partypersistence.NewSQLPartyStore(runtimeStore.DB(), "sqlite")
	service := partyservice.NewPartyDomainService(repository)
	if _, err := service.Upsert(t.Context(), "workspace", partymodel.Aggregate{
		Party:         partymodel.Party{ID: "party", Kind: partymodel.PartyKindPerson, DisplayName: "Person"},
		Person:        &partymodel.Person{GivenName: "Person"},
		ContactPoints: []partymodel.ContactPoint{{ID: "contact", Type: "email", Value: "person@example.com"}},
		Addresses:     []partymodel.Address{{ID: "address", Type: "home", Line1: "Street"}},
		Identifiers:   []partymodel.Identifier{{ID: "identifier", Type: "customer", Value: "1"}},
		CommunicationPreferences: []partymodel.CommunicationPreference{{
			ID: "preference", Channel: "email", Allowed: true,
		}},
		Consents: []partymodel.Consent{{
			ID: "consent", Purpose: "marketing", Status: "granted", Source: "web", CapturedAt: "now",
		}},
		PrivacyPreferences: []partymodel.PrivacyPreference{{
			ID: "privacy", Key: "tracking", Value: "denied", UpdatedAt: "now",
		}},
		MarketingSubscriptions: []partymodel.MarketingSubscription{{
			ID: "subscription", Channel: "email", Topic: "news", Status: "subscribed",
			ContactPointID: "contact", Source: "web", SubscribedAt: "now",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upsert(t.Context(), "workspace", partymodel.Aggregate{
		Party:        partymodel.Party{ID: "party", Kind: partymodel.PartyKindOrganization, DisplayName: "Organization"},
		Organization: &partymodel.Organization{LegalName: "Organization Ltd"},
	}); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := repository.Get(t.Context(), "workspace", "party")
	if err != nil || !found || loaded.Person != nil || loaded.Organization == nil {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}
	var personRows int
	if err := runtimeStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM party_persons WHERE workspace_id = ? AND party_id = ?`, "workspace", "party").Scan(&personRows); err != nil || personRows != 0 {
		t.Fatalf("person rows=%d err=%v", personRows, err)
	}
	for _, table := range []string{
		"party_contact_points", "party_addresses", "party_identifiers", "party_communication_preferences",
		"party_consents", "party_privacy_preferences", "party_marketing_subscriptions",
	} {
		var rows int
		query := `SELECT COUNT(*) FROM ` + table + ` WHERE workspace_id = ? AND party_id = ?`
		if err := runtimeStore.DB().QueryRowContext(t.Context(), query, "workspace", "party").Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("%s rows=%d err=%v", table, rows, err)
		}
	}
}

func TestSQLPartyStoreRejectsMissingWorkspace(t *testing.T) {
	repository := partypersistence.NewSQLPartyStore(nil, "sqlite")
	if _, err := repository.List(t.Context(), " "); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("list error=%v", err)
	}
	if _, _, err := repository.Get(t.Context(), "", "party"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("get error=%v", err)
	}
	if _, err := repository.Upsert(t.Context(), "", partymodel.Aggregate{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("upsert error=%v", err)
	}
}

func TestSQLPartyStorePersistsJobAndPositionCatalogWithWorkspaceIsolation(t *testing.T) {
	runtimeStore, err := runtimedatabase.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := partypersistence.NewSQLPartyStore(runtimeStore.DB(), "sqlite")
	service := partyservice.NewPartyCatalogDomainService(repository)
	for _, workspace := range []string{"workspace-a", "workspace-b"} {
		if _, err := service.UpsertJob(t.Context(), workspace, partymodel.JobCatalogItem{ID: "job", Code: "ENG", Name: workspace + " Engineer"}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.UpsertPosition(t.Context(), workspace, partymodel.Position{
			ID: "position", Code: "ENG-1", Name: workspace + " Position", JobCatalogItemID: "job", Headcount: 2,
		}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := service.ListJobs(t.Context(), "workspace-a")
	if err != nil || len(jobs) != 1 || jobs[0].Name != "workspace-a Engineer" {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	position, found, err := service.GetPosition(t.Context(), "workspace-a", "position")
	if err != nil || !found || position.Name != "workspace-a Position" || position.Headcount != 2 {
		t.Fatalf("position=%#v found=%v err=%v", position, found, err)
	}
	positions, err := service.ListPositions(t.Context(), "workspace-b")
	if err != nil || len(positions) != 1 || positions[0].Name != "workspace-b Position" {
		t.Fatalf("positions=%#v err=%v", positions, err)
	}
	if _, found, err := service.GetJob(t.Context(), "workspace-a", "missing"); err != nil || found {
		t.Fatalf("missing job found=%v err=%v", found, err)
	}
}

func TestSQLPartyStoreResolvesTrustedOrganizationScopeMemberships(t *testing.T) {
	runtimeStore, err := runtimedatabase.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := partypersistence.NewSQLPartyStore(runtimeStore.DB(), "sqlite")
	service := partyservice.NewPartyCatalogDomainService(repository)
	for _, extension := range []partymodel.OrganizationExtension{
		{ID: "territory", Kind: "territory", Code: "T", Name: "Territory"},
		{ID: "team", Kind: "team", Code: "TEAM", Name: "Team"},
		{ID: "store", Kind: "store", Code: "STORE", Name: "Store"},
		{ID: "warehouse", Kind: "warehouse", Code: "WAREHOUSE", Name: "Warehouse", ClaimValue: "warehouse_location_north_record"},
	} {
		if _, err := service.UpsertOrganizationExtension(t.Context(), "workspace", extension); err != nil {
			t.Fatal(err)
		}
	}
	for _, membership := range []partymodel.OrganizationExtensionMembership{
		{ID: "m-territory", ExtensionID: "territory", WorkforceProfileID: "workforce"},
		{ID: "m-team", ExtensionID: "team", WorkforceProfileID: "workforce"},
		{ID: "m-store", ExtensionID: "store", WorkforceProfileID: "workforce"},
		{ID: "m-warehouse", ExtensionID: "warehouse", WorkforceProfileID: "workforce"},
		{ID: "m-future", ExtensionID: "team", WorkforceProfileID: "future", EffectiveFrom: "2999-01-01T00:00:00Z"},
	} {
		if _, err := service.UpsertOrganizationExtensionMembership(t.Context(), "workspace", membership); err != nil {
			t.Fatal(err)
		}
	}
	facts, err := repository.ResolveIdentityOrganizationScopes(t.Context(), "workspace", []string{"workforce", "future"})
	if err != nil || len(facts.TeamIDs) != 1 || facts.TeamIDs[0] != "team" ||
		len(facts.StoreIDs) != 1 || facts.StoreIDs[0] != "store" ||
		len(facts.TerritoryIDs) != 1 || facts.TerritoryIDs[0] != "territory" ||
		len(facts.WarehouseIDs) != 1 || facts.WarehouseIDs[0] != "warehouse_location_north_record" {
		t.Fatalf("facts=%#v err=%v", facts, err)
	}
	if values, err := service.ListOrganizationExtensions(t.Context(), "workspace"); err != nil || len(values) != 4 {
		t.Fatalf("extensions=%#v err=%v", values, err)
	}
	if values, err := service.ListOrganizationExtensionMemberships(t.Context(), "workspace", "workforce"); err != nil || len(values) != 4 {
		t.Fatalf("memberships=%#v err=%v", values, err)
	}
	if facts, err := repository.ResolveIdentityOrganizationScopes(t.Context(), "other", []string{"workforce"}); err != nil || len(facts.TeamIDs) != 0 {
		t.Fatalf("cross-workspace facts=%#v err=%v", facts, err)
	}
}
