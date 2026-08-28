package service

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	partyrepository "github.com/domainry/domainry-runtime/runtime/domain/party/repository"
)

type PartyDomainService struct {
	repository partyrepository.PartyRepository
}

func NewPartyDomainService(repository partyrepository.PartyRepository) *PartyDomainService {
	return &PartyDomainService{repository: repository}
}

func (s *PartyDomainService) List(ctx context.Context, workspaceID string) ([]partymodel.Aggregate, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	return s.repository.List(ctx, workspaceID)
}

func (s *PartyDomainService) Get(ctx context.Context, workspaceID, partyID string) (partymodel.Aggregate, bool, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return partymodel.Aggregate{}, false, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	if strings.TrimSpace(partyID) == "" {
		return partymodel.Aggregate{}, false, partyError(apperror.KindBadRequest, "backend.party.id_required")
	}
	return s.repository.Get(ctx, workspaceID, strings.TrimSpace(partyID))
}

func (s *PartyDomainService) Upsert(ctx context.Context, workspaceID string, value partymodel.Aggregate) (partymodel.Aggregate, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return partymodel.Aggregate{}, partyError(apperror.KindForbidden, "backend.workspace_scope_required")
	}
	normalized, err := normalizeAggregate(value)
	if err != nil {
		return partymodel.Aggregate{}, err
	}
	return s.repository.Upsert(ctx, workspaceID, normalized)
}

func normalizeAggregate(value partymodel.Aggregate) (partymodel.Aggregate, error) {
	value.Party.ID = strings.TrimSpace(value.Party.ID)
	value.Party.Kind = strings.TrimSpace(value.Party.Kind)
	value.Party.DisplayName = strings.TrimSpace(value.Party.DisplayName)
	value.Party.Status = strings.TrimSpace(value.Party.Status)
	if value.Party.ID == "" || value.Party.DisplayName == "" {
		return partymodel.Aggregate{}, partyError(apperror.KindBadRequest, "backend.party.identity_required")
	}
	if value.Party.Status == "" {
		value.Party.Status = partymodel.PartyStatusActive
	}
	if value.Party.Status != partymodel.PartyStatusActive && value.Party.Status != partymodel.PartyStatusInactive {
		return partymodel.Aggregate{}, partyError(apperror.KindBadRequest, "backend.party.status_invalid")
	}
	switch value.Party.Kind {
	case partymodel.PartyKindPerson:
		if value.Person == nil || value.Organization != nil {
			return partymodel.Aggregate{}, partyError(apperror.KindBadRequest, "backend.party.person_shape_invalid")
		}
		value.Person.PartyID = value.Party.ID
		value.Person.GivenName = strings.TrimSpace(value.Person.GivenName)
		value.Person.FamilyName = strings.TrimSpace(value.Person.FamilyName)
	case partymodel.PartyKindOrganization:
		if value.Organization == nil || value.Person != nil {
			return partymodel.Aggregate{}, partyError(apperror.KindBadRequest, "backend.party.organization_shape_invalid")
		}
		value.Organization.PartyID = value.Party.ID
		value.Organization.LegalName = strings.TrimSpace(value.Organization.LegalName)
		value.Organization.RegistrationNumber = strings.TrimSpace(value.Organization.RegistrationNumber)
		if value.Organization.LegalName == "" {
			return partymodel.Aggregate{}, partyError(apperror.KindBadRequest, "backend.party.legal_name_required")
		}
	default:
		return partymodel.Aggregate{}, partyError(apperror.KindBadRequest, "backend.party.kind_invalid")
	}
	if err := normalizePartyChildren(&value); err != nil {
		return partymodel.Aggregate{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if value.Party.CreatedAt == "" {
		value.Party.CreatedAt = now
	}
	value.Party.UpdatedAt = now
	if value.Party.Version < 1 {
		value.Party.Version = 1
	}
	return value, nil
}

func normalizePartyChildren(value *partymodel.Aggregate) error {
	ids := map[string]bool{}
	register := func(id, kind string) (string, error) {
		id = strings.TrimSpace(id)
		if id == "" {
			return "", partyError(apperror.KindBadRequest, "backend.party."+kind+"_id_required")
		}
		if ids[id] {
			return "", partyError(apperror.KindConflict, "backend.party.child_id_conflict")
		}
		ids[id] = true
		return id, nil
	}
	for index := range value.ContactPoints {
		item := &value.ContactPoints[index]
		id, err := register(item.ID, "contact_point")
		if err != nil {
			return err
		}
		item.ID, item.PartyID = id, value.Party.ID
		item.Type, item.Value, item.Label, item.Status = strings.TrimSpace(item.Type), strings.TrimSpace(item.Value), strings.TrimSpace(item.Label), partyChildStatus(item.Status)
		if item.Type == "" || item.Value == "" {
			return partyError(apperror.KindBadRequest, "backend.party.contact_point_required")
		}
	}
	for index := range value.Addresses {
		item := &value.Addresses[index]
		id, err := register(item.ID, "address")
		if err != nil {
			return err
		}
		item.ID, item.PartyID = id, value.Party.ID
		item.Type, item.Line1, item.Status = strings.TrimSpace(item.Type), strings.TrimSpace(item.Line1), partyChildStatus(item.Status)
		if item.Type == "" || item.Line1 == "" {
			return partyError(apperror.KindBadRequest, "backend.party.address_required")
		}
	}
	for index := range value.Identifiers {
		item := &value.Identifiers[index]
		id, err := register(item.ID, "identifier")
		if err != nil {
			return err
		}
		item.ID, item.PartyID = id, value.Party.ID
		item.Type, item.Value, item.Issuer, item.Status = strings.TrimSpace(item.Type), strings.TrimSpace(item.Value), strings.TrimSpace(item.Issuer), partyChildStatus(item.Status)
		if item.Type == "" || item.Value == "" {
			return partyError(apperror.KindBadRequest, "backend.party.identifier_required")
		}
	}
	for index := range value.CommunicationPreferences {
		item := &value.CommunicationPreferences[index]
		id, err := register(item.ID, "communication_preference")
		if err != nil {
			return err
		}
		item.ID, item.PartyID = id, value.Party.ID
		item.Channel, item.Locale = strings.TrimSpace(item.Channel), strings.TrimSpace(item.Locale)
		if item.Channel == "" || item.Preferred && !item.Allowed {
			return partyError(apperror.KindBadRequest, "backend.party.communication_preference_invalid")
		}
	}
	contactPointIDs := map[string]bool{}
	for _, item := range value.ContactPoints {
		contactPointIDs[item.ID] = true
	}
	for index := range value.Consents {
		item := &value.Consents[index]
		id, err := register(item.ID, "consent")
		if err != nil {
			return err
		}
		item.ID, item.PartyID = id, value.Party.ID
		item.Purpose, item.Status = strings.TrimSpace(item.Purpose), strings.TrimSpace(item.Status)
		item.LegalBasis, item.Source = strings.TrimSpace(item.LegalBasis), strings.TrimSpace(item.Source)
		item.CapturedAt, item.ExpiresAt, item.PolicyVersion = strings.TrimSpace(item.CapturedAt), strings.TrimSpace(item.ExpiresAt), strings.TrimSpace(item.PolicyVersion)
		if item.Purpose == "" || item.Source == "" || item.CapturedAt == "" ||
			item.Status != "granted" && item.Status != "denied" && item.Status != "revoked" {
			return partyError(apperror.KindBadRequest, "backend.party.consent_invalid")
		}
	}
	for index := range value.PrivacyPreferences {
		item := &value.PrivacyPreferences[index]
		id, err := register(item.ID, "privacy_preference")
		if err != nil {
			return err
		}
		item.ID, item.PartyID = id, value.Party.ID
		item.Key, item.Value, item.UpdatedAt = strings.TrimSpace(item.Key), strings.TrimSpace(item.Value), strings.TrimSpace(item.UpdatedAt)
		if item.Key == "" || item.Value == "" || item.UpdatedAt == "" {
			return partyError(apperror.KindBadRequest, "backend.party.privacy_preference_invalid")
		}
	}
	for index := range value.MarketingSubscriptions {
		item := &value.MarketingSubscriptions[index]
		id, err := register(item.ID, "marketing_subscription")
		if err != nil {
			return err
		}
		item.ID, item.PartyID = id, value.Party.ID
		item.Channel, item.Topic, item.Status = strings.TrimSpace(item.Channel), strings.TrimSpace(item.Topic), strings.TrimSpace(item.Status)
		item.ContactPointID, item.Source = strings.TrimSpace(item.ContactPointID), strings.TrimSpace(item.Source)
		item.SubscribedAt, item.UnsubscribedAt = strings.TrimSpace(item.SubscribedAt), strings.TrimSpace(item.UnsubscribedAt)
		if item.Channel == "" || item.Topic == "" || item.Source == "" ||
			item.Status != "subscribed" && item.Status != "unsubscribed" ||
			item.Status == "subscribed" && item.SubscribedAt == "" ||
			item.Status == "unsubscribed" && item.UnsubscribedAt == "" {
			return partyError(apperror.KindBadRequest, "backend.party.marketing_subscription_invalid")
		}
		if item.ContactPointID != "" && !contactPointIDs[item.ContactPointID] {
			return partyError(apperror.KindBadRequest, "backend.party.marketing_contact_point_not_found")
		}
	}
	return nil
}

func partyChildStatus(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return partymodel.PartyStatusActive
}

func partyError(kind apperror.ErrorKind, code string) error {
	return &apperror.AppError{Kind: kind, Code: code}
}
