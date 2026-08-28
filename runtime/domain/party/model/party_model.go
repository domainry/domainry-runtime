package partymodel

const (
	PartyObjectKey        = "party"
	PersonObjectKey       = "person"
	OrganizationObjectKey = "organization"

	PartyKindPerson       = "person"
	PartyKindOrganization = "organization"

	PartyStatusActive   = "active"
	PartyStatusInactive = "inactive"
)

// Party is the shared identity of a person or organization. It deliberately
// has no login, workforce, role, or business-profile fields.
type Party struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	Version     int64  `json:"version"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type Person struct {
	PartyID    string `json:"party_id"`
	GivenName  string `json:"given_name,omitempty"`
	FamilyName string `json:"family_name,omitempty"`
	BirthDate  string `json:"birth_date,omitempty"`
}

type Organization struct {
	PartyID            string `json:"party_id"`
	LegalName          string `json:"legal_name"`
	RegistrationNumber string `json:"registration_number,omitempty"`
}

type Aggregate struct {
	Party                    Party                     `json:"party"`
	Person                   *Person                   `json:"person,omitempty"`
	Organization             *Organization             `json:"organization,omitempty"`
	ContactPoints            []ContactPoint            `json:"contact_points"`
	Addresses                []Address                 `json:"addresses"`
	Identifiers              []Identifier              `json:"identifiers"`
	CommunicationPreferences []CommunicationPreference `json:"communication_preferences"`
	Consents                 []Consent                 `json:"consents"`
	PrivacyPreferences       []PrivacyPreference       `json:"privacy_preferences"`
	MarketingSubscriptions   []MarketingSubscription   `json:"marketing_subscriptions"`
}

type ContactPoint struct {
	ID         string `json:"id"`
	PartyID    string `json:"party_id"`
	Type       string `json:"type"`
	Value      string `json:"value"`
	Label      string `json:"label,omitempty"`
	Primary    bool   `json:"primary,omitempty"`
	VerifiedAt string `json:"verified_at,omitempty"`
	Status     string `json:"status"`
}

type Address struct {
	ID         string `json:"id"`
	PartyID    string `json:"party_id"`
	Type       string `json:"type"`
	Line1      string `json:"line1"`
	Line2      string `json:"line2,omitempty"`
	Locality   string `json:"locality,omitempty"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Country    string `json:"country,omitempty"`
	Primary    bool   `json:"primary,omitempty"`
	Status     string `json:"status"`
}

type Identifier struct {
	ID      string `json:"id"`
	PartyID string `json:"party_id"`
	Type    string `json:"type"`
	Value   string `json:"value"`
	Issuer  string `json:"issuer,omitempty"`
	Status  string `json:"status"`
}

type CommunicationPreference struct {
	ID        string `json:"id"`
	PartyID   string `json:"party_id"`
	Channel   string `json:"channel"`
	Allowed   bool   `json:"allowed"`
	Preferred bool   `json:"preferred,omitempty"`
	Locale    string `json:"locale,omitempty"`
}

type Consent struct {
	ID            string `json:"id"`
	PartyID       string `json:"party_id"`
	Purpose       string `json:"purpose"`
	Status        string `json:"status"`
	LegalBasis    string `json:"legal_basis,omitempty"`
	Source        string `json:"source"`
	CapturedAt    string `json:"captured_at"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	PolicyVersion string `json:"policy_version,omitempty"`
}

type PrivacyPreference struct {
	ID        string `json:"id"`
	PartyID   string `json:"party_id"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

type MarketingSubscription struct {
	ID             string `json:"id"`
	PartyID        string `json:"party_id"`
	Channel        string `json:"channel"`
	Topic          string `json:"topic"`
	Status         string `json:"status"`
	ContactPointID string `json:"contact_point_id,omitempty"`
	Source         string `json:"source"`
	SubscribedAt   string `json:"subscribed_at,omitempty"`
	UnsubscribedAt string `json:"unsubscribed_at,omitempty"`
}
