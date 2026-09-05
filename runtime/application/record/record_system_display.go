package record

import (
	"context"
	"sort"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// resolveRecordSystemDisplayNames enriches public record read models only.
// Stable Identity IDs remain the source of truth and the resolved names are
// deliberately not persisted or exposed to Action inputs.
func (s *RecordApplicationService) resolveRecordSystemDisplayNames(ctx context.Context, records []recordmodel.Record) {
	if len(records) == 0 || s == nil || s.IdentityProjection() == nil {
		return
	}
	resolver, ok := s.IdentityProjection().(identitysdk.DisplayNameProjection)
	if !ok {
		return
	}
	userIDs := map[string]bool{}
	organizationUnitIDs := map[string]bool{}
	for _, record := range records {
		appendRecordDisplayID(userIDs, record.CreateBy)
		appendRecordDisplayID(userIDs, record.UpdateBy)
		appendRecordDisplayID(userIDs, record.OwnerUserID)
		appendRecordDisplayID(organizationUnitIDs, record.OwnerOrgID)
	}
	if len(userIDs) == 0 && len(organizationUnitIDs) == 0 {
		return
	}
	result, err := resolver.ResolveDisplayNames(ctx, identitysdk.DisplayNameQuery{
		UserIDs:             sortedRecordDisplayIDs(userIDs),
		OrganizationUnitIDs: sortedRecordDisplayIDs(organizationUnitIDs),
	})
	if err != nil {
		// Display names are a best-effort read projection. Identity outages or
		// deleted principals must not make otherwise-readable records unavailable.
		return
	}
	userNames := recordDisplayNameMap(result.Users)
	organizationUnitNames := recordDisplayNameMap(result.OrganizationUnits)
	for index := range records {
		records[index].CreateByName = userNames[strings.TrimSpace(records[index].CreateBy)]
		records[index].UpdateByName = userNames[strings.TrimSpace(records[index].UpdateBy)]
		records[index].OwnerUserName = userNames[strings.TrimSpace(records[index].OwnerUserID)]
		records[index].OwnerOrgName = organizationUnitNames[strings.TrimSpace(records[index].OwnerOrgID)]
	}
}

func appendRecordDisplayID(target map[string]bool, value string) {
	if value = strings.TrimSpace(value); value != "" {
		target[value] = true
	}
}

func sortedRecordDisplayIDs(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func recordDisplayNameMap(values []identitysdk.DisplayName) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value.ID)
		name := strings.TrimSpace(value.Name)
		if id != "" && name != "" {
			result[id] = name
		}
	}
	return result
}
