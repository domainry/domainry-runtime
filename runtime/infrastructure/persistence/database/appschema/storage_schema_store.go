package appschema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

func (s ApplicationSchemaStore) SyncManifestStorage(ctx context.Context, manifest manifestmodel.ManifestSchema) error {
	for _, object := range manifest.Objects {
		if strings.TrimSpace(object.Key) == "" {
			continue
		}
		if err := s.ensureObjectStorage(ctx, object); err != nil {
			return err
		}
	}
	return nil
}

func metadataFieldIndexed(field definitionmodel.FieldSchema) bool {
	if field.Unique {
		return true
	}
	raw, present := field.Config["indexed"]
	if !present {
		return strings.TrimSpace(field.Type) == "relation"
	}
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true") || strings.TrimSpace(value) == "1"
	default:
		return false
	}
}

func (s ApplicationSchemaStore) metadataFieldIndexName(table string, field string, unique bool) string {
	prefix := "idx"
	if unique {
		prefix = "uidx"
	}
	hash := sha256.Sum256([]byte(strings.TrimSpace(table) + "|" + strings.TrimSpace(field)))
	return prefix + "_field_" + hex.EncodeToString(hash[:])[:16]
}

// uniqueIndexName returns a deterministic index name for a composite_unique validation.
func (s ApplicationSchemaStore) uniqueIndexName(table string, fields []string) string {
	h := sha256.New()
	h.Write([]byte(table))
	for _, f := range fields {
		h.Write([]byte("|" + strings.TrimSpace(f)))
	}
	return "uidx_" + table + "_" + hex.EncodeToString(h.Sum(nil))[:10]
}

func (s ApplicationSchemaStore) conditionalUniqueIndexName(table string, policy recordvalidation.RecordConditionalUniquePolicy) string {
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(table) + "|" + strings.TrimSpace(policy.Key)))
	for _, field := range policy.Fields {
		h.Write([]byte("|field:" + strings.TrimSpace(field)))
	}
	h.Write([]byte("|when:" + strings.TrimSpace(policy.ConditionField)))
	for _, value := range policy.ConditionValues {
		h.Write([]byte("|value:" + value))
	}
	return "uidx_conditional_" + hex.EncodeToString(h.Sum(nil))[:16]
}

func (s ApplicationSchemaStore) temporalExclusionIndexName(table, policyKey string, fields []string) string {
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(table) + "|" + strings.TrimSpace(policyKey)))
	for _, field := range fields {
		h.Write([]byte("|" + strings.TrimSpace(field)))
	}
	return "idx_temporal_" + hex.EncodeToString(h.Sum(nil))[:16]
}

func (s ApplicationSchemaStore) relatedAggregateIndexName(table, policyKey string, fields []string) string {
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(table) + "|" + strings.TrimSpace(policyKey)))
	for _, field := range fields {
		h.Write([]byte("|" + strings.TrimSpace(field)))
	}
	return "idx_aggregate_" + hex.EncodeToString(h.Sum(nil))[:16]
}

func (s ApplicationSchemaStore) metadataIDColumnType() string {
	return s.storage.IDColumnType()
}

func (s ApplicationSchemaStore) metadataSQLTypeForField(field definitionmodel.FieldSchema) string {
	return s.storage.FieldColumnType(field, metadataFieldIndexed(field))
}
