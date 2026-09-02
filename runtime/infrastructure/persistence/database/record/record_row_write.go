package record

import (
	"github.com/domainry/domainry-orm/query"
	recordschema "github.com/domainry/domainry-orm/recordschema"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"strings"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func stringsJoinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		values = append(values, store.Identifier(column))
	}
	return strings.Join(values, ", ")
}

func recordFieldIsSystemOwned(fieldKey string) bool {
	_, owned := recordschema.SystemColumn(fieldKey)
	return owned
}

func appendRecordInsertMetadata(columns []string, values []any, record recordmodel.Record) ([]string, []any, error) {
	if record.Deleted {
		columns, values = append(columns, "deleted"), append(values, true)
	}
	if record.ExtInfo != nil {
		encoded, err := recordExtInfoDBValue(record.ExtInfo)
		if err != nil {
			return nil, nil, err
		}
		columns, values = append(columns, "ext_info"), append(values, encoded)
	}
	if userID := strings.TrimSpace(record.CreateBy); userID != "" {
		columns, values = append(columns, "create_by"), append(values, userID)
	}
	if userID := strings.TrimSpace(record.UpdateBy); userID != "" {
		columns, values = append(columns, "update_by"), append(values, userID)
	}
	if userID := strings.TrimSpace(record.OwnerUserID); userID != "" {
		columns, values = append(columns, "owner_user_id"), append(values, userID)
	}
	if orgID := strings.TrimSpace(record.OwnerOrgID); orgID != "" {
		columns, values = append(columns, "owner_org_id"), append(values, orgID)
	}
	return columns, values, nil
}

func applyRecordUpdateBuilder(builder *query.UpdateBuilder, record recordmodel.Record, writeDeleted bool) error {
	if writeDeleted {
		builder.Set("deleted", record.Deleted)
	}
	if record.ExtInfo != nil {
		encoded, err := recordExtInfoDBValue(record.ExtInfo)
		if err != nil {
			return err
		}
		builder.Set("ext_info", encoded)
	}
	if userID := strings.TrimSpace(record.UpdateBy); userID != "" {
		builder.Set("update_by", userID)
	}
	if userID := strings.TrimSpace(record.OwnerUserID); userID != "" {
		builder.Set("owner_user_id", userID)
	}
	if orgID := strings.TrimSpace(record.OwnerOrgID); orgID != "" {
		builder.Set("owner_org_id", orgID)
	}
	return nil
}

func appendRecordUpdateMetadata(store *database.RuntimeStore, assignments []string, values []any, record recordmodel.Record, writeDeleted bool) ([]string, []any, error) {
	if writeDeleted {
		assignments = append(assignments, store.Identifier("deleted")+" = "+store.Placeholder(len(values)+1))
		values = append(values, record.Deleted)
	}
	if record.ExtInfo != nil {
		encoded, err := recordExtInfoDBValue(record.ExtInfo)
		if err != nil {
			return nil, nil, err
		}
		assignments = append(assignments, store.Identifier("ext_info")+" = "+store.Placeholder(len(values)+1))
		values = append(values, encoded)
	}
	if userID := strings.TrimSpace(record.UpdateBy); userID != "" {
		assignments = append(assignments, store.Identifier("update_by")+" = "+store.Placeholder(len(values)+1))
		values = append(values, userID)
	}
	if userID := strings.TrimSpace(record.OwnerUserID); userID != "" {
		assignments = append(assignments, store.Identifier("owner_user_id")+" = "+store.Placeholder(len(values)+1))
		values = append(values, userID)
	}
	if orgID := strings.TrimSpace(record.OwnerOrgID); orgID != "" {
		assignments = append(assignments, store.Identifier("owner_org_id")+" = "+store.Placeholder(len(values)+1))
		values = append(values, orgID)
	}
	return assignments, values, nil
}

func stringsJoinPlaceholders(store *database.RuntimeStore, count int) string {
	values := make([]string, 0, count)
	for position := 1; position <= count; position++ {
		values = append(values, store.Placeholder(position))
	}
	return strings.Join(values, ", ")
}
