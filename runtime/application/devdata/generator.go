// Package devdata generates disposable development records from the validated
// project object model. It deliberately has no authored seed schema: model.json
// describes storage and this package derives sample values at runtime.
package devdata

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

const (
	defaultRecordsPerObject = 3
	maxRecordsPerObject     = 25
)

type Options struct {
	Seed             int64
	RecordsPerObject int
}

type Result struct {
	Generated int
	Skipped   bool
	Reason    string
}

// Store is the application-owned persistence port for disposable development
// records. Transaction lifecycle and SQL stay behind the bootstrap adapter.
type Store interface {
	ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
	InsertRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error
	InTransaction(context.Context, func(context.Context) error) error
}

// GenerateIfEmpty generates one coherent graph in a single transaction. A
// non-empty eligible object suppresses the whole run, so restarting Runtime
// can never append another batch or mix generated data with user data.
func GenerateIfEmpty(ctx context.Context, store Store, workspaceID string, model projectmodel.RuntimeModel, options Options) (Result, error) {
	if store == nil {
		return Result{}, fmt.Errorf("development data requires a Runtime store")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return Result{}, fmt.Errorf("development data requires a Workspace ID")
	}
	count := options.RecordsPerObject
	if count <= 0 {
		count = defaultRecordsPerObject
	}
	if count > maxRecordsPerObject {
		count = maxRecordsPerObject
	}
	seed := options.Seed
	if seed == 0 {
		var seedBytes [8]byte
		if _, err := cryptorand.Read(seedBytes[:]); err != nil {
			return Result{}, fmt.Errorf("generate development data random seed: %w", err)
		}
		seed = int64(binary.LittleEndian.Uint64(seedBytes[:]) & uint64(^uint64(0)>>1))
		if seed == 0 {
			seed = 1
		}
	}
	objects := eligibleObjects(model.Objects)
	if len(objects) == 0 {
		return Result{Skipped: true, Reason: "no_eligible_objects"}, nil
	}
	for _, object := range objects {
		page, err := store.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 1, SkipTotal: true})
		if err != nil {
			return Result{}, fmt.Errorf("inspect development data target %s: %w", object.Key, err)
		}
		if len(page.Items) != 0 {
			return Result{Skipped: true, Reason: "business_data_exists"}, nil
		}
	}

	random := rand.New(rand.NewSource(seed))
	idsByObject := make(map[string][]string, len(objects))
	generated := 0
	err := store.InTransaction(ctx, func(txCtx context.Context) error {
		for _, object := range objects {
			for index := 0; index < count; index++ {
				id := deterministicID(seed, object.Key, index)
				data, generateErr := generatedData(random, object, index, idsByObject)
				if generateErr != nil {
					return fmt.Errorf("generate development data for %s: %w", object.Key, generateErr)
				}
				now := time.Date(2026, time.January, 1, 0, 0, index, 0, time.UTC).Format(time.RFC3339Nano)
				record := recordmodel.Record{WorkspaceID: workspaceID, ID: id, Data: data, CreatedAt: now, UpdatedAt: now}
				if insertErr := store.InsertRecord(txCtx, workspaceID, object, record); insertErr != nil {
					return fmt.Errorf("insert development data for %s: %w", object.Key, insertErr)
				}
				idsByObject[object.Key] = append(idsByObject[object.Key], id)
				generated++
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Generated: generated}, nil
}

func eligibleObjects(input []definitionmodel.ObjectSchema) []definitionmodel.ObjectSchema {
	byKey := map[string]definitionmodel.ObjectSchema{}
	for _, object := range input {
		if objectEligible(object) {
			byKey[object.Key] = object
		}
	}
	// Repeatedly remove objects whose required relation points outside the
	// eligible graph. This includes Identity/external ownership and unsafe
	// objects removed in a prior pass.
	for changed := true; changed; {
		changed = false
		for key, object := range byKey {
			for _, field := range object.Fields {
				if !field.Required || strings.TrimSpace(field.Type) != "relation" {
					continue
				}
				target := relationTarget(field)
				if _, ok := byKey[target]; !ok {
					delete(byKey, key)
					changed = true
					break
				}
			}
		}
	}
	ordered := make([]definitionmodel.ObjectSchema, 0, len(byKey))
	resolved := map[string]bool{}
	for len(ordered) < len(byKey) {
		keys := make([]string, 0, len(byKey))
		for key := range byKey {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		progress := false
		for _, key := range keys {
			if resolved[key] {
				continue
			}
			ready := true
			for _, field := range byKey[key].Fields {
				if field.Required && strings.TrimSpace(field.Type) == "relation" && !resolved[relationTarget(field)] {
					ready = false
					break
				}
			}
			if ready {
				ordered = append(ordered, byKey[key])
				resolved[key] = true
				progress = true
			}
		}
		if !progress {
			// Required relation cycles have no safe first row.
			break
		}
	}
	return ordered
}

func objectEligible(object definitionmodel.ObjectSchema) bool {
	if object.Config["runtime_owned"] == true {
		return false
	}
	capabilities := definitionmodel.EffectiveObjectCapabilities(object)
	if !capabilities.Create || object.LedgerPolicy != nil || object.LifecyclePolicy != nil && object.LifecyclePolicy.Mode == definitionmodel.ObjectLifecycleAppendOnly {
		return false
	}
	for _, field := range object.Fields {
		if !field.Required {
			continue
		}
		kind := strings.TrimSpace(field.Type)
		if field.Sensitive || kind == recordmodel.RecordFileFieldType || kind == recordmodel.RecordFileListFieldType || kind == recordmodel.RecordJSONFieldType || kind == recordmodel.RecordMultiSelectFieldType || kind == "user" {
			return false
		}
		if kind == "select" && len(field.Validation.Options) == 0 && field.Default == nil && field.DefaultValue == nil {
			return false
		}
	}
	return true
}

func generatedData(random *rand.Rand, object definitionmodel.ObjectSchema, row int, idsByObject map[string][]string) (map[string]any, error) {
	data := map[string]any{}
	recordpolicy.RecordApplyFieldDefaults(object, data)
	for fieldIndex, field := range object.Fields {
		if field.Sensitive || strings.TrimSpace(field.DisabledAt) != "" {
			delete(data, field.Key)
			continue
		}
		if !recordvalidation.RecordIsEmptyValue(data[field.Key]) {
			continue
		}
		value, ok := generatedFieldValue(random, object, field, row, fieldIndex, idsByObject)
		if ok {
			data[field.Key] = value
		}
	}
	normalized, err := recordvalidation.RecordNormalizeData(object, data, false)
	if err != nil {
		return nil, err
	}
	if err := recordvalidation.RecordValidateData(object, normalized, false); err != nil {
		return nil, err
	}
	return normalized, nil
}

func generatedFieldValue(random *rand.Rand, object definitionmodel.ObjectSchema, field definitionmodel.FieldSchema, row, fieldIndex int, idsByObject map[string][]string) (any, bool) {
	kind := strings.TrimSpace(field.Type)
	suffix := strconv.Itoa(row+1) + "-" + strconv.Itoa(random.Intn(9000)+1000)
	switch kind {
	case "relation":
		ids := idsByObject[relationTarget(field)]
		if len(ids) == 0 {
			return nil, false
		}
		return ids[row%len(ids)], true
	case "select":
		if len(field.Validation.Options) == 0 {
			return nil, false
		}
		return field.Validation.Options[row%len(field.Validation.Options)], true
	case "boolean":
		return row%2 == 0, true
	case "integer":
		return int64(row + fieldIndex + 1), true
	case "number":
		return float64(row+1) + float64(fieldIndex)/10, true
	case "currency", "percent":
		return strconv.FormatFloat(float64(row+1)+float64(fieldIndex)/10, 'f', 2, 64), true
	case "date":
		return time.Date(2026, time.January, 1+row+fieldIndex, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), true
	case "datetime":
		return time.Date(2026, time.January, 1+row, fieldIndex, 0, 0, 0, time.UTC).Format(time.RFC3339), true
	case "email":
		return object.Key + "." + field.Key + "." + suffix + "@example.test", true
	case "phone":
		return "+86138" + fmt.Sprintf("%08d", random.Intn(100000000)), true
	case "url":
		return "https://example.test/" + object.Key + "/" + suffix, true
	case "text", "long_text", "":
		value := strings.TrimSpace(field.Name)
		if value == "" {
			value = field.Key
		}
		value += " " + suffix
		if max := field.Validation.MaxLength; max > 0 && len(value) > max {
			value = value[:max]
		}
		for len(value) < field.Validation.MinLength {
			value += "x"
		}
		return value, true
	default:
		return nil, false
	}
}

func relationTarget(field definitionmodel.FieldSchema) string {
	if field.Config == nil {
		return ""
	}
	for _, key := range []string{"target", "target_object_key", "object"} {
		if value := strings.TrimSpace(fmt.Sprint(field.Config[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return strings.TrimSpace(field.Validation.Target)
}

func deterministicID(seed int64, objectKey string, row int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%d", seed, objectKey, row)))
	return "dev_" + objectKey + "_" + hex.EncodeToString(digest[:8])
}
