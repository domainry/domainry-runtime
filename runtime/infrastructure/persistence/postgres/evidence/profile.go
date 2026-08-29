package evidence

import (
	"context"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }
func (Profile) Types(text string) persistencedriver.EvidenceSchemaTypes {
	return persistencedriver.EvidenceSchemaTypes{LargeText: "TEXT", IdempotencyScope: text, AuditCursor: text, RetirementEngine: text, RetirementNamespace: text, RetirementKind: text, RetirementObject: text}
}
func (Profile) Normalize(context.Context, persistencedriver.SchemaDatabase, ormdialect.Renderer) error {
	return nil
}
