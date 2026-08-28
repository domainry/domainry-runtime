package party

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	partyrepository "github.com/domainry/domainry-runtime/runtime/domain/party/repository"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type SQLPartyStore struct {
	db     *sql.DB
	driver string
	schema string
}

var _ partyrepository.PartyRepository = (*SQLPartyStore)(nil)

func NewSQLPartyStore(db *sql.DB, driver string, schema ...string) *SQLPartyStore {
	databaseSchema := ""
	if len(schema) != 0 {
		databaseSchema = strings.TrimSpace(schema[0])
	}
	return &SQLPartyStore{db: db, driver: strings.TrimSpace(driver), schema: databaseSchema}
}

func (s *SQLPartyStore) List(ctx context.Context, workspaceID string) ([]partymodel.Aggregate, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return nil, err
	}
	query := "SELECT " + s.columns("id", "kind", "display_name", "status", "version", "created_at", "updated_at") +
		" FROM " + s.table("party_parties") + " WHERE " + s.identifier("workspace_id") + " = " + s.placeholder(1)
	rows, err := s.db.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list parties: %w", err)
	}
	defer rows.Close()
	values := []partymodel.Aggregate{}
	for rows.Next() {
		value := partymodel.Aggregate{}
		if err := rows.Scan(&value.Party.ID, &value.Party.Kind, &value.Party.DisplayName, &value.Party.Status, &value.Party.Version, &value.Party.CreatedAt, &value.Party.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan party: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate parties: %w", err)
	}
	for index := range values {
		if err := s.loadDetail(ctx, workspaceID, &values[index]); err != nil {
			return nil, err
		}
	}
	sort.Slice(values, func(left, right int) bool { return values[left].Party.ID < values[right].Party.ID })
	return values, nil
}

func (s *SQLPartyStore) Get(ctx context.Context, workspaceID, partyID string) (partymodel.Aggregate, bool, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return partymodel.Aggregate{}, false, err
	}
	query := "SELECT " + s.columns("id", "kind", "display_name", "status", "version", "created_at", "updated_at") +
		" FROM " + s.table("party_parties") + " WHERE " + s.identifier("workspace_id") + " = " + s.placeholder(1) +
		" AND " + s.identifier("id") + " = " + s.placeholder(2)
	value := partymodel.Aggregate{}
	err := s.db.QueryRowContext(ctx, query, workspaceID, strings.TrimSpace(partyID)).Scan(
		&value.Party.ID, &value.Party.Kind, &value.Party.DisplayName, &value.Party.Status, &value.Party.Version, &value.Party.CreatedAt, &value.Party.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return partymodel.Aggregate{}, false, nil
	}
	if err != nil {
		return partymodel.Aggregate{}, false, fmt.Errorf("get party: %w", err)
	}
	if err := s.loadDetail(ctx, workspaceID, &value); err != nil {
		return partymodel.Aggregate{}, false, err
	}
	return value, true, nil
}

func (s *SQLPartyStore) Upsert(ctx context.Context, workspaceID string, value partymodel.Aggregate) (partymodel.Aggregate, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return partymodel.Aggregate{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return partymodel.Aggregate{}, fmt.Errorf("begin party upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range []string{
		"party_contact_points", "party_addresses", "party_identifiers", "party_communication_preferences",
		"party_consents", "party_privacy_preferences", "party_marketing_subscriptions",
		"party_persons", "party_organizations", "party_parties",
	} {
		idColumn := "party_id"
		if table == "party_parties" {
			idColumn = "id"
		}
		query := "DELETE FROM " + s.table(table) + " WHERE " + s.identifier("workspace_id") + " = " + s.placeholder(1) +
			" AND " + s.identifier(idColumn) + " = " + s.placeholder(2)
		if _, err := tx.ExecContext(ctx, query, workspaceID, value.Party.ID); err != nil {
			return partymodel.Aggregate{}, fmt.Errorf("replace party: %w", err)
		}
	}
	query := "INSERT INTO " + s.table("party_parties") + " (" + s.columns("id", "workspace_id", "kind", "display_name", "status", "version", "created_at", "updated_at") +
		") VALUES (" + s.placeholders(8) + ")"
	if _, err := tx.ExecContext(ctx, query, value.Party.ID, workspaceID, value.Party.Kind, value.Party.DisplayName, value.Party.Status, value.Party.Version, value.Party.CreatedAt, value.Party.UpdatedAt); err != nil {
		return partymodel.Aggregate{}, fmt.Errorf("write party: %w", err)
	}
	if value.Person != nil {
		query = "INSERT INTO " + s.table("party_persons") + " (" + s.columns("party_id", "workspace_id", "given_name", "family_name", "birth_date") + ") VALUES (" + s.placeholders(5) + ")"
		_, err = tx.ExecContext(ctx, query, value.Party.ID, workspaceID, value.Person.GivenName, value.Person.FamilyName, value.Person.BirthDate)
	} else {
		query = "INSERT INTO " + s.table("party_organizations") + " (" + s.columns("party_id", "workspace_id", "legal_name", "registration_number") + ") VALUES (" + s.placeholders(4) + ")"
		_, err = tx.ExecContext(ctx, query, value.Party.ID, workspaceID, value.Organization.LegalName, value.Organization.RegistrationNumber)
	}
	if err != nil {
		return partymodel.Aggregate{}, fmt.Errorf("write party detail: %w", err)
	}
	if err := s.writeChildren(ctx, tx, workspaceID, value); err != nil {
		return partymodel.Aggregate{}, err
	}
	if err := tx.Commit(); err != nil {
		return partymodel.Aggregate{}, fmt.Errorf("commit party upsert: %w", err)
	}
	return value, nil
}

func (s *SQLPartyStore) loadDetail(ctx context.Context, workspaceID string, value *partymodel.Aggregate) error {
	if value.Party.Kind == partymodel.PartyKindPerson {
		detail := &partymodel.Person{}
		query := "SELECT " + s.columns("party_id", "given_name", "family_name", "birth_date") + " FROM " + s.table("party_persons") +
			" WHERE " + s.identifier("workspace_id") + " = " + s.placeholder(1) + " AND " + s.identifier("party_id") + " = " + s.placeholder(2)
		if err := s.db.QueryRowContext(ctx, query, workspaceID, value.Party.ID).Scan(&detail.PartyID, &detail.GivenName, &detail.FamilyName, &detail.BirthDate); err != nil {
			return fmt.Errorf("get person detail: %w", err)
		}
		value.Person = detail
	} else {
		detail := &partymodel.Organization{}
		query := "SELECT " + s.columns("party_id", "legal_name", "registration_number") + " FROM " + s.table("party_organizations") +
			" WHERE " + s.identifier("workspace_id") + " = " + s.placeholder(1) + " AND " + s.identifier("party_id") + " = " + s.placeholder(2)
		if err := s.db.QueryRowContext(ctx, query, workspaceID, value.Party.ID).Scan(&detail.PartyID, &detail.LegalName, &detail.RegistrationNumber); err != nil {
			return fmt.Errorf("get organization detail: %w", err)
		}
		value.Organization = detail
	}
	return s.loadChildren(ctx, workspaceID, value)
}

func (s *SQLPartyStore) writeChildren(ctx context.Context, tx *sql.Tx, workspaceID string, value partymodel.Aggregate) error {
	contactPoints := make([][]any, 0, len(value.ContactPoints))
	for _, item := range value.ContactPoints {
		contactPoints = append(contactPoints, []any{item.ID, workspaceID, value.Party.ID, item.Type, item.Value, item.Label, item.Primary, item.VerifiedAt, item.Status})
	}
	if err := s.insertChildRows(ctx, tx, "party_contact_points", []string{"id", "workspace_id", "party_id", "type", "value", "label", "is_primary", "verified_at", "status"}, contactPoints); err != nil {
		return fmt.Errorf("write party contact points: %w", err)
	}
	addresses := make([][]any, 0, len(value.Addresses))
	for _, item := range value.Addresses {
		addresses = append(addresses, []any{item.ID, workspaceID, value.Party.ID, item.Type, item.Line1, item.Line2, item.Locality, item.Region, item.PostalCode, item.Country, item.Primary, item.Status})
	}
	if err := s.insertChildRows(ctx, tx, "party_addresses", []string{"id", "workspace_id", "party_id", "type", "line1", "line2", "locality", "region", "postal_code", "country", "is_primary", "status"}, addresses); err != nil {
		return fmt.Errorf("write party addresses: %w", err)
	}
	identifiers := make([][]any, 0, len(value.Identifiers))
	for _, item := range value.Identifiers {
		identifiers = append(identifiers, []any{item.ID, workspaceID, value.Party.ID, item.Type, item.Value, item.Issuer, item.Status})
	}
	if err := s.insertChildRows(ctx, tx, "party_identifiers", []string{"id", "workspace_id", "party_id", "type", "value", "issuer", "status"}, identifiers); err != nil {
		return fmt.Errorf("write party identifiers: %w", err)
	}
	communications := make([][]any, 0, len(value.CommunicationPreferences))
	for _, item := range value.CommunicationPreferences {
		communications = append(communications, []any{item.ID, workspaceID, value.Party.ID, item.Channel, item.Allowed, item.Preferred, item.Locale})
	}
	if err := s.insertChildRows(ctx, tx, "party_communication_preferences", []string{"id", "workspace_id", "party_id", "channel", "allowed", "preferred", "locale"}, communications); err != nil {
		return fmt.Errorf("write party communication preferences: %w", err)
	}
	consents := make([][]any, 0, len(value.Consents))
	for _, item := range value.Consents {
		consents = append(consents, []any{item.ID, workspaceID, value.Party.ID, item.Purpose, item.Status, item.LegalBasis, item.Source, item.CapturedAt, item.ExpiresAt, item.PolicyVersion})
	}
	if err := s.insertChildRows(ctx, tx, "party_consents", []string{"id", "workspace_id", "party_id", "purpose", "status", "legal_basis", "source", "captured_at", "expires_at", "policy_version"}, consents); err != nil {
		return fmt.Errorf("write party consents: %w", err)
	}
	privacy := make([][]any, 0, len(value.PrivacyPreferences))
	for _, item := range value.PrivacyPreferences {
		privacy = append(privacy, []any{item.ID, workspaceID, value.Party.ID, item.Key, item.Value, item.UpdatedAt})
	}
	if err := s.insertChildRows(ctx, tx, "party_privacy_preferences", []string{"id", "workspace_id", "party_id", "preference_key", "value", "updated_at"}, privacy); err != nil {
		return fmt.Errorf("write party privacy preferences: %w", err)
	}
	marketing := make([][]any, 0, len(value.MarketingSubscriptions))
	for _, item := range value.MarketingSubscriptions {
		marketing = append(marketing, []any{item.ID, workspaceID, value.Party.ID, item.Channel, item.Topic, item.Status, item.ContactPointID, item.Source, item.SubscribedAt, item.UnsubscribedAt})
	}
	if err := s.insertChildRows(ctx, tx, "party_marketing_subscriptions", []string{"id", "workspace_id", "party_id", "channel", "topic", "status", "contact_point_id", "source", "subscribed_at", "unsubscribed_at"}, marketing); err != nil {
		return fmt.Errorf("write party marketing subscriptions: %w", err)
	}
	return nil
}

func (s *SQLPartyStore) insertChildRows(ctx context.Context, tx *sql.Tx, table string, columns []string, values [][]any) error {
	for start := 0; start < len(values); start += 50 {
		end := min(start+50, len(values))
		rows := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*len(columns))
		for _, values := range values[start:end] {
			placeholders := make([]string, len(columns))
			for index := range placeholders {
				placeholders[index] = s.placeholder(len(args) + index + 1)
			}
			rows = append(rows, "("+strings.Join(placeholders, ", ")+")")
			args = append(args, values...)
		}
		query := "INSERT INTO " + s.table(table) + " (" + s.columns(columns...) + ") VALUES " + strings.Join(rows, ", ")
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLPartyStore) loadChildren(ctx context.Context, workspaceID string, value *partymodel.Aggregate) error {
	contactRows, err := s.db.QueryContext(ctx, "SELECT "+s.columns("id", "party_id", "type", "value", "label", "is_primary", "verified_at", "status")+" FROM "+s.table("party_contact_points")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("party_id")+" = "+s.placeholder(2), workspaceID, value.Party.ID)
	if err != nil {
		return fmt.Errorf("list party contact points: %w", err)
	}
	for contactRows.Next() {
		item := partymodel.ContactPoint{}
		if err := contactRows.Scan(&item.ID, &item.PartyID, &item.Type, &item.Value, &item.Label, &item.Primary, &item.VerifiedAt, &item.Status); err != nil {
			_ = contactRows.Close()
			return fmt.Errorf("scan party contact point: %w", err)
		}
		value.ContactPoints = append(value.ContactPoints, item)
	}
	if err := closePartyRows(contactRows, "contact points"); err != nil {
		return err
	}
	addressRows, err := s.db.QueryContext(ctx, "SELECT "+s.columns("id", "party_id", "type", "line1", "line2", "locality", "region", "postal_code", "country", "is_primary", "status")+" FROM "+s.table("party_addresses")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("party_id")+" = "+s.placeholder(2), workspaceID, value.Party.ID)
	if err != nil {
		return fmt.Errorf("list party addresses: %w", err)
	}
	for addressRows.Next() {
		item := partymodel.Address{}
		if err := addressRows.Scan(&item.ID, &item.PartyID, &item.Type, &item.Line1, &item.Line2, &item.Locality, &item.Region, &item.PostalCode, &item.Country, &item.Primary, &item.Status); err != nil {
			_ = addressRows.Close()
			return fmt.Errorf("scan party address: %w", err)
		}
		value.Addresses = append(value.Addresses, item)
	}
	if err := closePartyRows(addressRows, "addresses"); err != nil {
		return err
	}
	identifierRows, err := s.db.QueryContext(ctx, "SELECT "+s.columns("id", "party_id", "type", "value", "issuer", "status")+" FROM "+s.table("party_identifiers")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("party_id")+" = "+s.placeholder(2), workspaceID, value.Party.ID)
	if err != nil {
		return fmt.Errorf("list party identifiers: %w", err)
	}
	for identifierRows.Next() {
		item := partymodel.Identifier{}
		if err := identifierRows.Scan(&item.ID, &item.PartyID, &item.Type, &item.Value, &item.Issuer, &item.Status); err != nil {
			_ = identifierRows.Close()
			return fmt.Errorf("scan party identifier: %w", err)
		}
		value.Identifiers = append(value.Identifiers, item)
	}
	if err := closePartyRows(identifierRows, "identifiers"); err != nil {
		return err
	}
	preferenceRows, err := s.db.QueryContext(ctx, "SELECT "+s.columns("id", "party_id", "channel", "allowed", "preferred", "locale")+" FROM "+s.table("party_communication_preferences")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("party_id")+" = "+s.placeholder(2), workspaceID, value.Party.ID)
	if err != nil {
		return fmt.Errorf("list party communication preferences: %w", err)
	}
	for preferenceRows.Next() {
		item := partymodel.CommunicationPreference{}
		if err := preferenceRows.Scan(&item.ID, &item.PartyID, &item.Channel, &item.Allowed, &item.Preferred, &item.Locale); err != nil {
			_ = preferenceRows.Close()
			return fmt.Errorf("scan party communication preference: %w", err)
		}
		value.CommunicationPreferences = append(value.CommunicationPreferences, item)
	}
	if err := closePartyRows(preferenceRows, "communication preferences"); err != nil {
		return err
	}
	consentRows, err := s.db.QueryContext(ctx, "SELECT "+s.columns("id", "party_id", "purpose", "status", "legal_basis", "source", "captured_at", "expires_at", "policy_version")+" FROM "+s.table("party_consents")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("party_id")+" = "+s.placeholder(2), workspaceID, value.Party.ID)
	if err != nil {
		return fmt.Errorf("list party consents: %w", err)
	}
	for consentRows.Next() {
		item := partymodel.Consent{}
		if err := consentRows.Scan(&item.ID, &item.PartyID, &item.Purpose, &item.Status, &item.LegalBasis, &item.Source, &item.CapturedAt, &item.ExpiresAt, &item.PolicyVersion); err != nil {
			_ = consentRows.Close()
			return fmt.Errorf("scan party consent: %w", err)
		}
		value.Consents = append(value.Consents, item)
	}
	if err := closePartyRows(consentRows, "consents"); err != nil {
		return err
	}
	privacyRows, err := s.db.QueryContext(ctx, "SELECT "+s.columns("id", "party_id", "preference_key", "value", "updated_at")+" FROM "+s.table("party_privacy_preferences")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("party_id")+" = "+s.placeholder(2), workspaceID, value.Party.ID)
	if err != nil {
		return fmt.Errorf("list party privacy preferences: %w", err)
	}
	for privacyRows.Next() {
		item := partymodel.PrivacyPreference{}
		if err := privacyRows.Scan(&item.ID, &item.PartyID, &item.Key, &item.Value, &item.UpdatedAt); err != nil {
			_ = privacyRows.Close()
			return fmt.Errorf("scan party privacy preference: %w", err)
		}
		value.PrivacyPreferences = append(value.PrivacyPreferences, item)
	}
	if err := closePartyRows(privacyRows, "privacy preferences"); err != nil {
		return err
	}
	subscriptionRows, err := s.db.QueryContext(ctx, "SELECT "+s.columns("id", "party_id", "channel", "topic", "status", "contact_point_id", "source", "subscribed_at", "unsubscribed_at")+" FROM "+s.table("party_marketing_subscriptions")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("party_id")+" = "+s.placeholder(2), workspaceID, value.Party.ID)
	if err != nil {
		return fmt.Errorf("list party marketing subscriptions: %w", err)
	}
	for subscriptionRows.Next() {
		item := partymodel.MarketingSubscription{}
		if err := subscriptionRows.Scan(&item.ID, &item.PartyID, &item.Channel, &item.Topic, &item.Status, &item.ContactPointID, &item.Source, &item.SubscribedAt, &item.UnsubscribedAt); err != nil {
			_ = subscriptionRows.Close()
			return fmt.Errorf("scan party marketing subscription: %w", err)
		}
		value.MarketingSubscriptions = append(value.MarketingSubscriptions, item)
	}
	if err := closePartyRows(subscriptionRows, "marketing subscriptions"); err != nil {
		return err
	}
	sort.Slice(value.ContactPoints, func(left, right int) bool { return value.ContactPoints[left].ID < value.ContactPoints[right].ID })
	sort.Slice(value.Addresses, func(left, right int) bool { return value.Addresses[left].ID < value.Addresses[right].ID })
	sort.Slice(value.Identifiers, func(left, right int) bool { return value.Identifiers[left].ID < value.Identifiers[right].ID })
	sort.Slice(value.CommunicationPreferences, func(left, right int) bool {
		return value.CommunicationPreferences[left].ID < value.CommunicationPreferences[right].ID
	})
	sort.Slice(value.Consents, func(left, right int) bool { return value.Consents[left].ID < value.Consents[right].ID })
	sort.Slice(value.PrivacyPreferences, func(left, right int) bool {
		return value.PrivacyPreferences[left].ID < value.PrivacyPreferences[right].ID
	})
	sort.Slice(value.MarketingSubscriptions, func(left, right int) bool {
		return value.MarketingSubscriptions[left].ID < value.MarketingSubscriptions[right].ID
	})
	return nil
}

func closePartyRows(rows *sql.Rows, label string) error {
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate party %s: %w", label, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close party %s: %w", label, err)
	}
	return nil
}

func requireWorkspace(value string) error {
	if strings.TrimSpace(value) == "" {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required"}
	}
	return nil
}

func (s *SQLPartyStore) identifier(value string) string {
	if s.driver == "mysql" {
		return mysql.Dialect{}.Identifier(value)
	}
	return sqlite.Dialect{}.Identifier(value)
}

func (s *SQLPartyStore) table(value string) string {
	if s.driver == "postgres" && s.schema != "" {
		return s.identifier(s.schema) + "." + s.identifier(value)
	}
	return s.identifier(value)
}

func (s *SQLPartyStore) placeholder(position int) string {
	if s.driver == "postgres" {
		return fmt.Sprintf("$%d", position)
	}
	return "?"
}

func (s *SQLPartyStore) placeholders(count int) string {
	values := make([]string, count)
	for index := range values {
		values[index] = s.placeholder(index + 1)
	}
	return strings.Join(values, ", ")
}

func (s *SQLPartyStore) columns(values ...string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = s.identifier(value)
	}
	return strings.Join(quoted, ", ")
}
