package datamigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	ormdialect "github.com/domainry/domainry-orm/dialect"
)

type CopyOptions struct {
	BatchSize  int
	Throttle   time.Duration
	MaxBatches int
}

type Checkpoint struct {
	Version         int                        `json:"version"`
	PlanFingerprint string                     `json:"plan_fingerprint"`
	UpdatedAt       time.Time                  `json:"updated_at"`
	State           string                     `json:"state"`
	Batches         int64                      `json:"batches"`
	Tables          map[string]TableCheckpoint `json:"tables"`
}

type TableCheckpoint struct {
	LastKey   json.RawMessage `json:"last_key,omitempty"`
	Processed int64           `json:"processed"`
	Digest    string          `json:"digest,omitempty"`
	Done      bool            `json:"done"`
}

type CheckpointStore interface {
	Load(context.Context) (Checkpoint, error)
	Save(context.Context, Checkpoint) error
}

type CopyLease interface {
	Acquire(context.Context, string) (CopyLeaseHandle, error)
}

type CopyLeaseHandle interface {
	Check(context.Context) error
	Release() error
}

type Copier struct {
	Source       *sql.DB
	Target       *sql.DB
	SourceEngine Engine
	TargetSchema string
	Plan         Plan
	Checkpoints  CheckpointStore
	Lease        CopyLease
	Options      CopyOptions
}

func (c Copier) Run(ctx context.Context) (Checkpoint, error) {
	if c.Source == nil || c.Target == nil || c.Checkpoints == nil {
		return Checkpoint{}, fmt.Errorf("data migration copier requires source, target, and checkpoint store")
	}
	if len(c.Plan.Blockers) > 0 {
		return Checkpoint{}, fmt.Errorf("data migration plan has blockers: %s", strings.Join(c.Plan.Blockers, "; "))
	}
	if !ormdialect.ValidIdentifier(c.TargetSchema) {
		return Checkpoint{}, fmt.Errorf("unsafe target schema identifier")
	}
	fingerprint := PlanFingerprint(c.Plan)
	var lease CopyLeaseHandle
	var err error
	if c.Lease != nil {
		lease, err = c.Lease.Acquire(ctx, fingerprint)
		if err != nil {
			return Checkpoint{}, fmt.Errorf("acquire data migration copy lease: %w", err)
		}
		defer func() { _ = lease.Release() }()
	}
	checkLease := func() error {
		if lease == nil {
			return nil
		}
		if err := lease.Check(ctx); err != nil {
			return fmt.Errorf("data migration copy lease lost: %w", err)
		}
		return nil
	}
	checkpoint, err := c.Checkpoints.Load(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	if checkpoint.PlanFingerprint != "" && checkpoint.PlanFingerprint != fingerprint {
		return Checkpoint{}, fmt.Errorf("data migration plan changed after checkpoint creation")
	}
	checkpoint.PlanFingerprint = fingerprint
	checkpoint.State = "running"
	if checkpoint.Tables == nil {
		checkpoint.Tables = map[string]TableCheckpoint{}
	}
	batchSize := c.Options.BatchSize
	if batchSize <= 0 || batchSize > 10000 {
		batchSize = 500
	}
	for _, tablePlan := range c.Plan.Tables {
		if err := checkLease(); err != nil {
			return checkpoint, err
		}
		if err := ctx.Err(); err != nil {
			return checkpoint, err
		}
		current := checkpoint.Tables[tablePlan.Name]
		if current.Done {
			continue
		}
		for {
			if err := checkLease(); err != nil {
				return checkpoint, err
			}
			processed, next, err := c.copyBatch(ctx, tablePlan, current, batchSize)
			if err != nil {
				return checkpoint, err
			}
			current = next
			if processed == 0 || processed < batchSize {
				current.Done = true
			}
			checkpoint.Tables[tablePlan.Name] = current
			checkpoint.Batches++
			if err := checkLease(); err != nil {
				return checkpoint, err
			}
			if err := c.Checkpoints.Save(ctx, checkpoint); err != nil {
				return checkpoint, err
			}
			if c.Options.MaxBatches > 0 && checkpoint.Batches >= int64(c.Options.MaxBatches) && !current.Done {
				checkpoint.State = "paused"
				if err := c.Checkpoints.Save(ctx, checkpoint); err != nil {
					return checkpoint, err
				}
				return checkpoint, nil
			}
			if current.Done {
				break
			}
			if c.Options.Throttle > 0 {
				timer := time.NewTimer(c.Options.Throttle)
				select {
				case <-ctx.Done():
					timer.Stop()
					return checkpoint, ctx.Err()
				case <-timer.C:
				}
			}
		}
	}
	checkpoint.State = "completed"
	if err := c.Checkpoints.Save(ctx, checkpoint); err != nil {
		return checkpoint, err
	}
	for _, sequence := range c.Plan.Sequences {
		if sequence.Blocked || !ormdialect.ValidIdentifier(sequence.TargetName) {
			return checkpoint, fmt.Errorf("sequence %s does not have a safe target mapping", sequence.SourceName)
		}
		if _, err := c.Target.ExecContext(ctx, "SELECT setval($1::regclass, $2, true)", c.TargetSchema+"."+sequence.TargetName, sequence.CurrentValue); err != nil {
			return checkpoint, fmt.Errorf("synchronize target sequence %s: %w", sequence.TargetName, err)
		}
	}
	return checkpoint, nil
}

func (c Copier) RunFinalDelta(ctx context.Context, cutoverEvidencePath string) (Checkpoint, error) {
	if _, err := ValidateCutoverEvidence(cutoverEvidencePath, time.Now().UTC()); err != nil {
		return Checkpoint{}, err
	}
	checkpoint, err := c.Checkpoints.Load(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	checkpoint.Tables = map[string]TableCheckpoint{}
	checkpoint.PlanFingerprint = ""
	if err := c.Checkpoints.Save(ctx, checkpoint); err != nil {
		return Checkpoint{}, err
	}
	return c.Run(ctx)
}

func (c Copier) copyBatch(ctx context.Context, tablePlan TablePlan, checkpoint TableCheckpoint, batchSize int) (int, TableCheckpoint, error) {
	if len(tablePlan.CheckpointKey) != 1 {
		return 0, checkpoint, fmt.Errorf("table %s does not have one checkpoint key", tablePlan.Name)
	}
	sourceTable, ok := findTable(c.Plan.Source.Tables, tablePlan.Name)
	if !ok {
		return 0, checkpoint, fmt.Errorf("source table %s is missing from plan", tablePlan.Name)
	}
	columns := make([]string, 0, len(sourceTable.Columns))
	for _, column := range sourceTable.Columns {
		if !ormdialect.ValidIdentifier(column.Name) {
			return 0, checkpoint, fmt.Errorf("unsafe source column identifier")
		}
		columns = append(columns, column.Name)
	}
	key := tablePlan.CheckpointKey[0]
	query := "SELECT " + joinQuoted(columns) + " FROM " + c.sourceRelation(tablePlan.Name)
	args := []any{}
	if len(checkpoint.LastKey) > 0 {
		var last any
		if err := json.Unmarshal(checkpoint.LastKey, &last); err != nil {
			return 0, checkpoint, fmt.Errorf("decode checkpoint key for %s: %w", tablePlan.Name, err)
		}
		query += " WHERE " + quote(key) + " > " + sourcePlaceholder(c.SourceEngine, 1)
		args = append(args, last)
	}
	query += " ORDER BY " + quote(key) + " ASC LIMIT " + strconvInt(batchSize)
	rows, err := c.Source.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, checkpoint, fmt.Errorf("read source batch %s: %w", tablePlan.Name, err)
	}
	defer rows.Close()
	tx, err := c.Target.BeginTx(ctx, nil)
	if err != nil {
		return 0, checkpoint, err
	}
	defer tx.Rollback()
	processed := 0
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return 0, checkpoint, err
		}
		converted, err := convertRow(sourceTable, tablePlan, columns, values)
		if err != nil {
			return 0, checkpoint, fmt.Errorf("convert %s row: %w", tablePlan.Name, err)
		}
		if tablePlan.WorkspaceScoped {
			workspaceIndex := indexOf(columns, "workspace_id")
			if workspaceIndex < 0 {
				return 0, checkpoint, fmt.Errorf("workspace-scoped table %s has no workspace_id column", tablePlan.Name)
			}
			workspace := converted[workspaceIndex]
			if strings.TrimSpace(fmt.Sprint(workspace)) == "" {
				return 0, checkpoint, fmt.Errorf("table %s contains a row without workspace_id", tablePlan.Name)
			}
		}
		if err := upsertPostgresRow(ctx, tx, c.TargetSchema, tablePlan.Name, columns, key, converted); err != nil {
			return 0, checkpoint, err
		}
		keyIndex := indexOf(columns, key)
		if keyIndex < 0 {
			return 0, checkpoint, fmt.Errorf("checkpoint key %s is missing from table %s", key, tablePlan.Name)
		}
		keyValue := converted[keyIndex]
		checkpoint.LastKey, _ = json.Marshal(keyValue)
		checkpoint.Digest = rollDigest(checkpoint.Digest, canonicalRow(converted))
		checkpoint.Processed++
		processed++
	}
	if err := rows.Err(); err != nil {
		return 0, checkpoint, err
	}
	if err := tx.Commit(); err != nil {
		return 0, checkpoint, err
	}
	return processed, checkpoint, nil
}

func convertRow(source TableInventory, plan TablePlan, columns []string, values []any) ([]any, error) {
	converted := append([]any(nil), values...)
	conversionByColumn := map[string]ConversionPlan{}
	for _, conversion := range plan.Conversions {
		conversionByColumn[conversion.Column] = conversion
	}
	for index, column := range columns {
		value := converted[index]
		if value == nil {
			continue
		}
		conversion := conversionByColumn[column]
		switch conversion.Strategy {
		case "identity", "blob_to_bytea":
		case "sqlite_zero_one_to_boolean":
			boolean, ok := parseSQLiteBoolean(value)
			if !ok {
				return nil, fmt.Errorf("column %s is not a reversible SQLite boolean", column)
			}
			converted[index] = boolean
		case "rfc3339_text_to_timestamptz":
			parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(fmt.Sprint(value)))
			if err != nil {
				return nil, fmt.Errorf("column %s is not RFC3339: %w", column, err)
			}
			converted[index] = parsed.UTC()
		case "validate_json_text":
			raw := []byte(fmt.Sprint(value))
			if !json.Valid(raw) {
				return nil, fmt.Errorf("column %s contains invalid JSON", column)
			}
			converted[index] = string(raw)
		default:
			return nil, fmt.Errorf("column %s requires unsupported conversion %s", column, conversion.Strategy)
		}
	}
	_ = source
	return converted, nil
}

func upsertPostgresRow(ctx context.Context, tx *sql.Tx, schema, table string, columns []string, key string, values []any) error {
	relation := quote(schema) + "." + quote(table)
	placeholders := make([]string, len(columns))
	updates := []string{}
	for index, column := range columns {
		placeholders[index] = "$" + strconvInt(index+1)
		if column != key {
			updates = append(updates, quote(column)+" = EXCLUDED."+quote(column))
		}
	}
	query := "INSERT INTO " + relation + " (" + joinQuoted(columns) + ") VALUES (" + strings.Join(placeholders, ", ") + ") ON CONFLICT (" + quote(key) + ") "
	if len(updates) == 0 {
		query += "DO NOTHING"
	} else {
		query += "DO UPDATE SET " + strings.Join(updates, ", ")
	}
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		return fmt.Errorf("upsert target table %s: %w", table, err)
	}
	return nil
}

func PlanFingerprint(plan Plan) string {
	stable := struct {
		Source    Engine         `json:"source_engine"`
		Target    Engine         `json:"target_engine"`
		Schema    string         `json:"schema"`
		Tables    []TablePlan    `json:"tables"`
		Sequences []SequencePlan `json:"sequences"`
	}{plan.Source.Engine, plan.Target.Engine, plan.Target.Schema, plan.Tables, plan.Sequences}
	raw, _ := json.Marshal(stable)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func rollDigest(previous string, row []byte) string {
	hash := sha256.New()
	if decoded, err := hex.DecodeString(previous); err == nil {
		_, _ = hash.Write(decoded)
	}
	_, _ = hash.Write(row)
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalRow(values []any) []byte {
	canonical := make([]string, len(values))
	for index, value := range values {
		switch typed := value.(type) {
		case nil:
			canonical[index] = "null:"
		case []byte:
			canonical[index] = "bytes:" + hex.EncodeToString(typed)
		case time.Time:
			canonical[index] = "time:" + typed.UTC().Format(time.RFC3339Nano)
		case bool:
			canonical[index] = fmt.Sprintf("bool:%t", typed)
		default:
			canonical[index] = fmt.Sprintf("value:%v", typed)
		}
	}
	raw, _ := json.Marshal(canonical)
	return raw
}

func (c Copier) sourceRelation(table string) string {
	if c.SourceEngine == EnginePostgres {
		return quote(c.Plan.Source.Schema) + "." + quote(table)
	}
	return quote(table)
}

func sourcePlaceholder(engine Engine, position int) string {
	if engine == EnginePostgres {
		return "$" + strconvInt(position)
	}
	return "?"
}

func joinQuoted(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = quote(value)
	}
	return strings.Join(quoted, ", ")
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}

func strconvInt(value int) string { return fmt.Sprintf("%d", value) }

func orderPlansByForeignKeys(source Inventory, plans []TablePlan) ([]TablePlan, []string) {
	byName := map[string]TablePlan{}
	dependencies := map[string]map[string]bool{}
	for _, plan := range plans {
		byName[plan.Name] = plan
		dependencies[plan.Name] = map[string]bool{}
	}
	for _, table := range source.Tables {
		for _, foreign := range table.ForeignKeys {
			if _, included := byName[foreign.ReferencedTable]; included && foreign.ReferencedTable != table.Name {
				dependencies[table.Name][foreign.ReferencedTable] = true
			}
		}
	}
	ordered := []TablePlan{}
	for len(ordered) < len(plans) {
		ready := []string{}
		for name, required := range dependencies {
			if len(required) == 0 {
				ready = append(ready, name)
			}
		}
		if len(ready) == 0 {
			remaining := make([]string, 0, len(dependencies))
			for name := range dependencies {
				remaining = append(remaining, name)
			}
			sort.Strings(remaining)
			return plans, []string{"foreign-key dependency cycle requires deferred constraints: " + strings.Join(remaining, ", ")}
		}
		sort.Strings(ready)
		for _, name := range ready {
			ordered = append(ordered, byName[name])
			delete(dependencies, name)
			for _, required := range dependencies {
				delete(required, name)
			}
		}
	}
	return ordered, nil
}
