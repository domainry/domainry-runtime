package service

import (
	"context"
	"errors"
	"testing"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type partyRepositoryStub struct {
	values []partymodel.Aggregate
	value  partymodel.Aggregate
	found  bool
	err    error
}

func (s *partyRepositoryStub) List(context.Context, string) ([]partymodel.Aggregate, error) {
	return s.values, s.err
}

func (s *partyRepositoryStub) Get(context.Context, string, string) (partymodel.Aggregate, bool, error) {
	return s.value, s.found, s.err
}

func (s *partyRepositoryStub) Upsert(_ context.Context, _ string, value partymodel.Aggregate) (partymodel.Aggregate, error) {
	s.value = value
	return value, s.err
}

func TestPartyDomainServiceValidatesScopeAndAggregateShape(t *testing.T) {
	repository := &partyRepositoryStub{}
	service := NewPartyDomainService(repository)
	if _, err := service.List(t.Context(), " "); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("list scope error=%v", err)
	}
	if _, _, err := service.Get(t.Context(), "workspace", " "); apperror.CodeOf(err) != "backend.party.id_required" {
		t.Fatalf("get id error=%v", err)
	}
	if _, _, err := service.Get(t.Context(), " ", "party"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("get scope error=%v", err)
	}
	cases := []struct {
		name string
		item partymodel.Aggregate
		code string
	}{
		{name: "identity", item: partymodel.Aggregate{}, code: "backend.party.identity_required"},
		{name: "status", item: partymodel.Aggregate{Party: partymodel.Party{ID: "p", Kind: "person", DisplayName: "P", Status: "deleted"}, Person: &partymodel.Person{}}, code: "backend.party.status_invalid"},
		{name: "kind", item: partymodel.Aggregate{Party: partymodel.Party{ID: "p", Kind: "other", DisplayName: "P"}}, code: "backend.party.kind_invalid"},
		{name: "person missing", item: partymodel.Aggregate{Party: partymodel.Party{ID: "p", Kind: "person", DisplayName: "P"}}, code: "backend.party.person_shape_invalid"},
		{name: "person mixed", item: partymodel.Aggregate{Party: partymodel.Party{ID: "p", Kind: "person", DisplayName: "P"}, Person: &partymodel.Person{}, Organization: &partymodel.Organization{}}, code: "backend.party.person_shape_invalid"},
		{name: "organization missing", item: partymodel.Aggregate{Party: partymodel.Party{ID: "p", Kind: "organization", DisplayName: "P"}}, code: "backend.party.organization_shape_invalid"},
		{name: "organization mixed", item: partymodel.Aggregate{Party: partymodel.Party{ID: "p", Kind: "organization", DisplayName: "P"}, Person: &partymodel.Person{}, Organization: &partymodel.Organization{}}, code: "backend.party.organization_shape_invalid"},
		{name: "legal name", item: partymodel.Aggregate{Party: partymodel.Party{ID: "p", Kind: "organization", DisplayName: "P"}, Organization: &partymodel.Organization{}}, code: "backend.party.legal_name_required"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Upsert(t.Context(), "workspace", test.item); apperror.CodeOf(err) != test.code {
				t.Fatalf("error=%v code=%q", err, apperror.CodeOf(err))
			}
		})
	}
	if _, err := service.Upsert(t.Context(), " ", partymodel.Aggregate{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("upsert scope error=%v", err)
	}
}

func TestPartyDomainServiceNormalizesPersonAndOrganization(t *testing.T) {
	repository := &partyRepositoryStub{}
	service := NewPartyDomainService(repository)
	person, err := service.Upsert(t.Context(), "workspace", partymodel.Aggregate{
		Party:  partymodel.Party{ID: " person ", Kind: "person", DisplayName: " Person "},
		Person: &partymodel.Person{PartyID: "wrong", GivenName: " Given ", FamilyName: " Family "},
	})
	if err != nil || person.Party.ID != "person" || person.Party.Status != "active" || person.Party.Version != 1 ||
		person.Party.CreatedAt == "" || person.Party.UpdatedAt == "" || person.Person.PartyID != "person" ||
		person.Person.GivenName != "Given" || person.Person.FamilyName != "Family" {
		t.Fatalf("person=%#v err=%v", person, err)
	}
	organization, err := service.Upsert(t.Context(), "workspace", partymodel.Aggregate{
		Party:        partymodel.Party{ID: "organization", Kind: "organization", DisplayName: "Organization", Status: "inactive", Version: 2, CreatedAt: "created"},
		Organization: &partymodel.Organization{LegalName: " Legal ", RegistrationNumber: " REG "},
	})
	if err != nil || organization.Party.Status != "inactive" || organization.Party.Version != 2 || organization.Party.CreatedAt != "created" ||
		organization.Organization.PartyID != "organization" || organization.Organization.LegalName != "Legal" ||
		organization.Organization.RegistrationNumber != "REG" {
		t.Fatalf("organization=%#v err=%v", organization, err)
	}
}

func TestPartyDomainServiceDelegatesRepositoryResults(t *testing.T) {
	fault := errors.New("fault")
	repository := &partyRepositoryStub{values: []partymodel.Aggregate{{Party: partymodel.Party{ID: "p"}}}, value: partymodel.Aggregate{Party: partymodel.Party{ID: "p"}}, found: true}
	service := NewPartyDomainService(repository)
	if values, err := service.List(t.Context(), "workspace"); err != nil || len(values) != 1 {
		t.Fatalf("list=%#v err=%v", values, err)
	}
	if value, found, err := service.Get(t.Context(), "workspace", " p "); err != nil || !found || value.Party.ID != "p" {
		t.Fatalf("get=%#v found=%v err=%v", value, found, err)
	}
	repository.err = fault
	if _, err := service.List(t.Context(), "workspace"); !errors.Is(err, fault) {
		t.Fatalf("list error=%v", err)
	}
	if _, _, err := service.Get(t.Context(), "workspace", "p"); !errors.Is(err, fault) {
		t.Fatalf("get error=%v", err)
	}
	value := partymodel.Aggregate{Party: partymodel.Party{ID: "p", Kind: "person", DisplayName: "P"}, Person: &partymodel.Person{}}
	if _, err := service.Upsert(t.Context(), "workspace", value); !errors.Is(err, fault) {
		t.Fatalf("upsert error=%v", err)
	}
}

func TestPartyDomainServiceNormalizesContactFoundationChildren(t *testing.T) {
	service := NewPartyDomainService(&partyRepositoryStub{})
	value, err := service.Upsert(t.Context(), "workspace", partymodel.Aggregate{
		Party:  partymodel.Party{ID: "party", Kind: "person", DisplayName: "Party"},
		Person: &partymodel.Person{},
		ContactPoints: []partymodel.ContactPoint{{
			ID: " email ", PartyID: "wrong", Type: " email ", Value: " person@example.com ", Label: " Work ",
		}},
		Addresses: []partymodel.Address{{
			ID: "address", PartyID: "wrong", Type: "home", Line1: "1 Main Street",
		}},
		Identifiers: []partymodel.Identifier{{
			ID: "identifier", PartyID: "wrong", Type: "customer_no", Value: " C-1 ", Issuer: " CRM ",
		}},
		CommunicationPreferences: []partymodel.CommunicationPreference{{
			ID: "preference", PartyID: "wrong", Channel: " email ", Allowed: true, Preferred: true, Locale: " en-US ",
		}},
	})
	if err != nil || value.ContactPoints[0].ID != "email" || value.ContactPoints[0].PartyID != "party" ||
		value.ContactPoints[0].Type != "email" || value.ContactPoints[0].Value != "person@example.com" ||
		value.ContactPoints[0].Label != "Work" || value.ContactPoints[0].Status != "active" ||
		value.Addresses[0].PartyID != "party" || value.Addresses[0].Status != "active" ||
		value.Identifiers[0].PartyID != "party" || value.Identifiers[0].Value != "C-1" || value.Identifiers[0].Issuer != "CRM" ||
		value.CommunicationPreferences[0].PartyID != "party" || value.CommunicationPreferences[0].Channel != "email" ||
		value.CommunicationPreferences[0].Locale != "en-US" {
		t.Fatalf("value=%#v err=%v", value, err)
	}
}

func TestPartyDomainServiceRejectsInvalidContactFoundationChildren(t *testing.T) {
	base := func() partymodel.Aggregate {
		return partymodel.Aggregate{Party: partymodel.Party{ID: "party", Kind: "person", DisplayName: "Party"}, Person: &partymodel.Person{}}
	}
	cases := []struct {
		name string
		edit func(*partymodel.Aggregate)
		code string
	}{
		{name: "contact id", edit: func(value *partymodel.Aggregate) {
			value.ContactPoints = []partymodel.ContactPoint{{Type: "email", Value: "a@example.com"}}
		}, code: "backend.party.contact_point_id_required"},
		{name: "contact value", edit: func(value *partymodel.Aggregate) { value.ContactPoints = []partymodel.ContactPoint{{ID: "contact"}} }, code: "backend.party.contact_point_required"},
		{name: "address id", edit: func(value *partymodel.Aggregate) {
			value.Addresses = []partymodel.Address{{Type: "home", Line1: "street"}}
		}, code: "backend.party.address_id_required"},
		{name: "address line", edit: func(value *partymodel.Aggregate) { value.Addresses = []partymodel.Address{{ID: "address"}} }, code: "backend.party.address_required"},
		{name: "identifier id", edit: func(value *partymodel.Aggregate) {
			value.Identifiers = []partymodel.Identifier{{Type: "customer", Value: "1"}}
		}, code: "backend.party.identifier_id_required"},
		{name: "identifier value", edit: func(value *partymodel.Aggregate) { value.Identifiers = []partymodel.Identifier{{ID: "identifier"}} }, code: "backend.party.identifier_required"},
		{name: "preference id", edit: func(value *partymodel.Aggregate) {
			value.CommunicationPreferences = []partymodel.CommunicationPreference{{Channel: "email"}}
		}, code: "backend.party.communication_preference_id_required"},
		{name: "preference channel", edit: func(value *partymodel.Aggregate) {
			value.CommunicationPreferences = []partymodel.CommunicationPreference{{ID: "preference"}}
		}, code: "backend.party.communication_preference_invalid"},
		{name: "preference contradiction", edit: func(value *partymodel.Aggregate) {
			value.CommunicationPreferences = []partymodel.CommunicationPreference{{ID: "preference", Channel: "email", Preferred: true}}
		}, code: "backend.party.communication_preference_invalid"},
		{name: "duplicate child", edit: func(value *partymodel.Aggregate) {
			value.ContactPoints = []partymodel.ContactPoint{{ID: "duplicate", Type: "email", Value: "a@example.com"}}
			value.Addresses = []partymodel.Address{{ID: "duplicate", Type: "home", Line1: "street"}}
		}, code: "backend.party.child_id_conflict"},
	}
	service := NewPartyDomainService(&partyRepositoryStub{})
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := base()
			test.edit(&value)
			if _, err := service.Upsert(t.Context(), "workspace", value); apperror.CodeOf(err) != test.code {
				t.Fatalf("error=%v code=%q", err, apperror.CodeOf(err))
			}
		})
	}
}

func TestPartyChildStatusPreservesExplicitStatus(t *testing.T) {
	repository := &partyRepositoryStub{}
	service := NewPartyDomainService(repository)
	value, err := service.Upsert(t.Context(), "workspace", partymodel.Aggregate{
		Party:  partymodel.Party{ID: "party", Kind: partymodel.PartyKindPerson, DisplayName: "Person"},
		Person: &partymodel.Person{GivenName: "Person"},
		ContactPoints: []partymodel.ContactPoint{{
			ID: "email", Type: "email", Value: "person@example.com", Status: "inactive",
		}},
	})
	if err != nil || value.ContactPoints[0].Status != "inactive" {
		t.Fatalf("value=%#v err=%v", value, err)
	}
}

func TestPartyDomainServiceNormalizesConsentPrivacyAndMarketing(t *testing.T) {
	service := NewPartyDomainService(&partyRepositoryStub{})
	value, err := service.Upsert(t.Context(), "workspace", partymodel.Aggregate{
		Party:  partymodel.Party{ID: "party", Kind: "person", DisplayName: "Party"},
		Person: &partymodel.Person{},
		ContactPoints: []partymodel.ContactPoint{{
			ID: "email", Type: "email", Value: "party@example.com",
		}},
		Consents: []partymodel.Consent{{
			ID: " consent ", PartyID: "wrong", Purpose: " marketing ", Status: "granted", LegalBasis: " consent ",
			Source: " web_form ", CapturedAt: " 2026-07-25T00:00:00Z ", PolicyVersion: " v1 ",
		}},
		PrivacyPreferences: []partymodel.PrivacyPreference{{
			ID: " privacy ", PartyID: "wrong", Key: " profiling ", Value: " denied ", UpdatedAt: " 2026-07-25T00:00:00Z ",
		}},
		MarketingSubscriptions: []partymodel.MarketingSubscription{{
			ID: " subscription ", PartyID: "wrong", Channel: " email ", Topic: " newsletter ", Status: "subscribed",
			ContactPointID: " email ", Source: " self_service ", SubscribedAt: " 2026-07-25T00:00:00Z ",
		}},
	})
	if err != nil || value.Consents[0].ID != "consent" || value.Consents[0].PartyID != "party" ||
		value.Consents[0].Purpose != "marketing" || value.Consents[0].Source != "web_form" ||
		value.PrivacyPreferences[0].Key != "profiling" || value.PrivacyPreferences[0].Value != "denied" ||
		value.MarketingSubscriptions[0].PartyID != "party" || value.MarketingSubscriptions[0].ContactPointID != "email" ||
		value.MarketingSubscriptions[0].Topic != "newsletter" {
		t.Fatalf("value=%#v err=%v", value, err)
	}
}

func TestPartyDomainServiceRejectsInvalidConsentPrivacyAndMarketing(t *testing.T) {
	base := func() partymodel.Aggregate {
		return partymodel.Aggregate{Party: partymodel.Party{ID: "party", Kind: "person", DisplayName: "Party"}, Person: &partymodel.Person{}}
	}
	cases := []struct {
		name string
		edit func(*partymodel.Aggregate)
		code string
	}{
		{name: "consent id", edit: func(value *partymodel.Aggregate) { value.Consents = []partymodel.Consent{{Purpose: "marketing"}} }, code: "backend.party.consent_id_required"},
		{name: "consent shape", edit: func(value *partymodel.Aggregate) {
			value.Consents = []partymodel.Consent{{ID: "consent", Purpose: "marketing", Status: "unknown"}}
		}, code: "backend.party.consent_invalid"},
		{name: "privacy id", edit: func(value *partymodel.Aggregate) {
			value.PrivacyPreferences = []partymodel.PrivacyPreference{{Key: "tracking"}}
		}, code: "backend.party.privacy_preference_id_required"},
		{name: "privacy shape", edit: func(value *partymodel.Aggregate) {
			value.PrivacyPreferences = []partymodel.PrivacyPreference{{ID: "privacy"}}
		}, code: "backend.party.privacy_preference_invalid"},
		{name: "subscription id", edit: func(value *partymodel.Aggregate) {
			value.MarketingSubscriptions = []partymodel.MarketingSubscription{{Channel: "email"}}
		}, code: "backend.party.marketing_subscription_id_required"},
		{name: "subscription shape", edit: func(value *partymodel.Aggregate) {
			value.MarketingSubscriptions = []partymodel.MarketingSubscription{{ID: "subscription"}}
		}, code: "backend.party.marketing_subscription_invalid"},
		{name: "subscribe timestamp", edit: func(value *partymodel.Aggregate) {
			value.MarketingSubscriptions = []partymodel.MarketingSubscription{{ID: "subscription", Channel: "email", Topic: "news", Status: "subscribed", Source: "web"}}
		}, code: "backend.party.marketing_subscription_invalid"},
		{name: "unsubscribe timestamp", edit: func(value *partymodel.Aggregate) {
			value.MarketingSubscriptions = []partymodel.MarketingSubscription{{ID: "subscription", Channel: "email", Topic: "news", Status: "unsubscribed", Source: "web"}}
		}, code: "backend.party.marketing_subscription_invalid"},
		{name: "unknown contact point", edit: func(value *partymodel.Aggregate) {
			value.MarketingSubscriptions = []partymodel.MarketingSubscription{{ID: "subscription", Channel: "email", Topic: "news", Status: "subscribed", Source: "web", SubscribedAt: "now", ContactPointID: "missing"}}
		}, code: "backend.party.marketing_contact_point_not_found"},
		{name: "cross kind duplicate", edit: func(value *partymodel.Aggregate) {
			value.Consents = []partymodel.Consent{{ID: "duplicate", Purpose: "marketing", Status: "granted", Source: "web", CapturedAt: "now"}}
			value.PrivacyPreferences = []partymodel.PrivacyPreference{{ID: "duplicate", Key: "tracking", Value: "denied", UpdatedAt: "now"}}
		}, code: "backend.party.child_id_conflict"},
	}
	service := NewPartyDomainService(&partyRepositoryStub{})
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := base()
			test.edit(&value)
			if _, err := service.Upsert(t.Context(), "workspace", value); apperror.CodeOf(err) != test.code {
				t.Fatalf("error=%v code=%q", err, apperror.CodeOf(err))
			}
		})
	}
}

func TestPartyDomainServiceRemainingValidationConditionOutcomes(t *testing.T) {
	base := func() partymodel.Aggregate {
		return partymodel.Aggregate{
			Party:  partymodel.Party{ID: "party", Kind: partymodel.PartyKindPerson, DisplayName: "Party"},
			Person: &partymodel.Person{},
		}
	}
	cases := []struct {
		name string
		edit func(*partymodel.Aggregate)
		code string
	}{
		{
			name: "display name",
			edit: func(value *partymodel.Aggregate) { value.Party.DisplayName = " " },
			code: "backend.party.identity_required",
		},
		{
			name: "contact value only",
			edit: func(value *partymodel.Aggregate) {
				value.ContactPoints = []partymodel.ContactPoint{{ID: "contact", Type: "email", Value: " "}}
			},
			code: "backend.party.contact_point_required",
		},
		{
			name: "address line only",
			edit: func(value *partymodel.Aggregate) {
				value.Addresses = []partymodel.Address{{ID: "address", Type: "home", Line1: " "}}
			},
			code: "backend.party.address_required",
		},
		{
			name: "identifier value only",
			edit: func(value *partymodel.Aggregate) {
				value.Identifiers = []partymodel.Identifier{{ID: "identifier", Type: "customer", Value: " "}}
			},
			code: "backend.party.identifier_required",
		},
		{
			name: "consent purpose only",
			edit: func(value *partymodel.Aggregate) {
				value.Consents = []partymodel.Consent{{ID: "consent", Purpose: " ", Source: "web", CapturedAt: "now", Status: "granted"}}
			},
			code: "backend.party.consent_invalid",
		},
		{
			name: "consent captured at only",
			edit: func(value *partymodel.Aggregate) {
				value.Consents = []partymodel.Consent{{ID: "consent", Purpose: "marketing", Source: "web", CapturedAt: " ", Status: "granted"}}
			},
			code: "backend.party.consent_invalid",
		},
		{
			name: "consent invalid status",
			edit: func(value *partymodel.Aggregate) {
				value.Consents = []partymodel.Consent{{ID: "consent", Purpose: "marketing", Source: "web", CapturedAt: "now", Status: "pending"}}
			},
			code: "backend.party.consent_invalid",
		},
		{
			name: "privacy value only",
			edit: func(value *partymodel.Aggregate) {
				value.PrivacyPreferences = []partymodel.PrivacyPreference{{ID: "privacy", Key: "tracking", Value: " ", UpdatedAt: "now"}}
			},
			code: "backend.party.privacy_preference_invalid",
		},
		{
			name: "privacy updated at only",
			edit: func(value *partymodel.Aggregate) {
				value.PrivacyPreferences = []partymodel.PrivacyPreference{{ID: "privacy", Key: "tracking", Value: "denied", UpdatedAt: " "}}
			},
			code: "backend.party.privacy_preference_invalid",
		},
		{
			name: "subscription topic only",
			edit: func(value *partymodel.Aggregate) {
				value.MarketingSubscriptions = []partymodel.MarketingSubscription{{ID: "subscription", Channel: "email", Topic: " ", Source: "web", Status: "subscribed", SubscribedAt: "now"}}
			},
			code: "backend.party.marketing_subscription_invalid",
		},
		{
			name: "subscription source only",
			edit: func(value *partymodel.Aggregate) {
				value.MarketingSubscriptions = []partymodel.MarketingSubscription{{ID: "subscription", Channel: "email", Topic: "news", Source: " ", Status: "subscribed", SubscribedAt: "now"}}
			},
			code: "backend.party.marketing_subscription_invalid",
		},
		{
			name: "subscription invalid status",
			edit: func(value *partymodel.Aggregate) {
				value.MarketingSubscriptions = []partymodel.MarketingSubscription{{ID: "subscription", Channel: "email", Topic: "news", Source: "web", Status: "paused", SubscribedAt: "now"}}
			},
			code: "backend.party.marketing_subscription_invalid",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := base()
			tc.edit(&value)
			if _, err := normalizeAggregate(value); apperror.CodeOf(err) != tc.code {
				t.Fatalf("error=%v code=%q", err, apperror.CodeOf(err))
			}
		})
	}

	for _, status := range []string{"denied", "revoked"} {
		t.Run("valid consent "+status, func(t *testing.T) {
			value := base()
			value.Consents = []partymodel.Consent{{
				ID: "consent", Purpose: "marketing", Source: "web", CapturedAt: "now", Status: status,
			}}
			if _, err := normalizeAggregate(value); err != nil {
				t.Fatalf("consent status %q rejected: %v", status, err)
			}
		})
	}

	value := base()
	value.CommunicationPreferences = []partymodel.CommunicationPreference{{
		ID: "preference", Channel: "email", Allowed: true, Preferred: false,
	}}
	value.MarketingSubscriptions = []partymodel.MarketingSubscription{{
		ID: "subscription", Channel: "email", Topic: "news", Source: "web",
		Status: "unsubscribed", UnsubscribedAt: "now",
	}}
	if _, err := normalizeAggregate(value); err != nil {
		t.Fatalf("valid unbound unsubscribe rejected: %v", err)
	}
}
