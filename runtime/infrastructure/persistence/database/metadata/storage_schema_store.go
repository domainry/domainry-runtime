package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

func (s MetadataStore) SyncManifestStorage(ctx context.Context, manifest manifestmodel.ManifestSchema) error {
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

func (s MetadataStore) metadataFieldIndexName(table string, field string, unique bool) string {
	prefix := "idx"
	if unique {
		prefix = "uidx"
	}
	hash := sha256.Sum256([]byte(strings.TrimSpace(table) + "|" + strings.TrimSpace(field)))
	return prefix + "_field_" + hex.EncodeToString(hash[:])[:16]
}

// uniqueIndexName returns a deterministic index name for a composite_unique validation.
func (s MetadataStore) uniqueIndexName(table string, fields []string) string {
	h := sha256.New()
	h.Write([]byte(table))
	for _, f := range fields {
		h.Write([]byte("|" + strings.TrimSpace(f)))
	}
	return "uidx_" + table + "_" + hex.EncodeToString(h.Sum(nil))[:10]
}

func (s MetadataStore) conditionalUniqueIndexName(table string, policy recordvalidation.RecordConditionalUniquePolicy) string {
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

func conditionalUniqueGuardColumn(indexName string) string {
	hash := sha256.Sum256([]byte(indexName))
	return "_domainry_cuq_" + hex.EncodeToString(hash[:])[:16]
}

func (s MetadataStore) temporalExclusionIndexName(table, policyKey string, fields []string) string {
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(table) + "|" + strings.TrimSpace(policyKey)))
	for _, field := range fields {
		h.Write([]byte("|" + strings.TrimSpace(field)))
	}
	return "idx_temporal_" + hex.EncodeToString(h.Sum(nil))[:16]
}

func (s MetadataStore) relatedAggregateIndexName(table, policyKey string, fields []string) string {
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(table) + "|" + strings.TrimSpace(policyKey)))
	for _, field := range fields {
		h.Write([]byte("|" + strings.TrimSpace(field)))
	}
	return "idx_aggregate_" + hex.EncodeToString(h.Sum(nil))[:16]
}

func (s MetadataStore) metadataIDColumnType() string {
	return metadataIDColumnTypeForDriver(s.store.Driver())
}

func metadataIDColumnTypeForDriver(driver string) string {
	if driver == "mysql" {
		// Runtime identifiers are stable keys, not free text. VARCHAR(191) keeps
		// four-column utf8mb4 composite indexes within InnoDB's 3072-byte limit.
		return "VARCHAR(191)"
	}
	return "TEXT"
}

func (s MetadataStore) metadataSQLTypeForField(field definitionmodel.FieldSchema) string {
	return metadataSQLTypeForFieldDriver(s.store.Driver(), field)
}

func metadataSQLTypeForFieldDriver(driver string, field definitionmodel.FieldSchema) string {
	switch strings.TrimSpace(field.Type) {
	case "integer":
		if driver == "sqlite" {
			return "INTEGER"
		}
		return "BIGINT"
	case "currency", "percent":
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			config, _ = recordmodel.RecordNormalizeDecimalConfig(nil)
		}
		if driver == "mysql" {
			return fmt.Sprintf("DECIMAL(%d,%d)", config.Precision, config.Scale)
		}
		if driver == "postgres" {
			return fmt.Sprintf("NUMERIC(%d,%d)", config.Precision, config.Scale)
		}
		return "TEXT"
	case "number":
		if driver == "mysql" {
			return "DOUBLE"
		}
		if driver == "postgres" {
			return "DOUBLE PRECISION"
		}
		return "REAL"
	case "boolean":
		if driver == "sqlite" {
			return "INTEGER"
		}
		return "BOOLEAN"
	case "json":
		if driver == "sqlite" {
			return "TEXT"
		}
		return "JSON"
	default:
		if driver == "mysql" && strings.TrimSpace(field.Type) != "long_text" && metadataFieldIndexed(field) {
			return fmt.Sprintf("VARCHAR(%d)", metadataMySQLIndexedLength(field))
		}
		return "TEXT"
	}
}

func metadataMySQLIndexedLength(field definitionmodel.FieldSchema) int {
	const maximum = 191
	value := 0
	switch typed := field.Config["max_length"].(type) {
	case int:
		value = typed
	case int64:
		value = int(typed)
	case float64:
		value = int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		value = int(parsed)
	}
	if value > 0 && value < maximum {
		return value
	}
	return maximum
}
