package datamigration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

type VerificationReport struct {
	VerifiedAt time.Time           `json:"verified_at"`
	Current    bool                `json:"current"`
	Tables     []TableVerification `json:"tables"`
	Errors     []string            `json:"errors,omitempty"`
}

type TableVerification struct {
	Name                    string `json:"name"`
	SourceRows              int64  `json:"source_rows"`
	TargetRows              int64  `json:"target_rows"`
	SourceKeys              int64  `json:"source_keys"`
	TargetKeys              int64  `json:"target_keys"`
	SourceDigest            string `json:"source_digest"`
	TargetDigest            string `json:"target_digest"`
	SourceSampleDigest      string `json:"source_sample_digest"`
	TargetSampleDigest      string `json:"target_sample_digest"`
	SourceDuplicateRows     int64  `json:"source_duplicate_rows"`
	TargetDuplicateRows     int64  `json:"target_duplicate_rows"`
	SourceInvalidWorkspaces int64  `json:"source_invalid_workspace_rows"`
	TargetInvalidWorkspaces int64  `json:"target_invalid_workspace_rows"`
	SourceInvalidReferences int64  `json:"source_invalid_reference_rows"`
	TargetInvalidReferences int64  `json:"target_invalid_reference_rows"`
	Current                 bool   `json:"current"`
}

type tableVerificationFacts struct {
	rows              int64
	keys              int64
	digest            string
	sampleDigest      string
	duplicateRows     int64
	invalidWorkspaces int64
	invalidReferences int64
}

func (c Copier) Verify(ctx context.Context) (VerificationReport, error) {
	report := VerificationReport{VerifiedAt: time.Now().UTC(), Current: true}
	for _, tablePlan := range c.Plan.Tables {
		if len(tablePlan.CheckpointKey) != 1 {
			return report, fmt.Errorf("table %s does not have one verification ordering key", tablePlan.Name)
		}
		sourceFacts, err := c.digestTable(ctx, c.Source, c.SourceEngine, c.Plan.Source.Schema, tablePlan, true)
		if err != nil {
			return report, err
		}
		targetFacts, err := c.digestTable(ctx, c.Target, EnginePostgres, c.TargetSchema, tablePlan, false)
		if err != nil {
			return report, err
		}
		current := sourceFacts.rows == targetFacts.rows && sourceFacts.keys == targetFacts.keys && sourceFacts.digest == targetFacts.digest && sourceFacts.sampleDigest == targetFacts.sampleDigest && sourceFacts.duplicateRows == 0 && targetFacts.duplicateRows == 0 && sourceFacts.invalidWorkspaces == 0 && targetFacts.invalidWorkspaces == 0 && sourceFacts.invalidReferences == 0 && targetFacts.invalidReferences == 0
		report.Tables = append(report.Tables, TableVerification{
			Name:       tablePlan.Name,
			SourceRows: sourceFacts.rows, TargetRows: targetFacts.rows,
			SourceKeys: sourceFacts.keys, TargetKeys: targetFacts.keys,
			SourceDigest: sourceFacts.digest, TargetDigest: targetFacts.digest,
			SourceSampleDigest: sourceFacts.sampleDigest, TargetSampleDigest: targetFacts.sampleDigest,
			SourceDuplicateRows: sourceFacts.duplicateRows, TargetDuplicateRows: targetFacts.duplicateRows,
			SourceInvalidWorkspaces: sourceFacts.invalidWorkspaces, TargetInvalidWorkspaces: targetFacts.invalidWorkspaces,
			SourceInvalidReferences: sourceFacts.invalidReferences, TargetInvalidReferences: targetFacts.invalidReferences,
			Current: current,
		})
		if !current {
			report.Current = false
			report.Errors = append(report.Errors, fmt.Sprintf("table %s row/key/digest/sample differs or contains invalid data", tablePlan.Name))
		}
	}
	return report, nil
}

func (c Copier) digestTable(ctx context.Context, db *sql.DB, engine Engine, schema string, tablePlan TablePlan, convertSource bool) (tableVerificationFacts, error) {
	sourceTable, ok := findTable(c.Plan.Source.Tables, tablePlan.Name)
	if !ok {
		return tableVerificationFacts{}, fmt.Errorf("source table %s missing from plan", tablePlan.Name)
	}
	columns := make([]string, 0, len(sourceTable.Columns))
	for _, column := range sourceTable.Columns {
		columns = append(columns, column.Name)
	}
	relation := verificationRelation(engine, schema, tablePlan.Name)
	query := "SELECT " + joinVerificationIdentifiers(engine, columns) + " FROM " + relation + " ORDER BY " + verificationIdentifier(engine, tablePlan.CheckpointKey[0]) + " ASC"
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return tableVerificationFacts{}, err
	}
	defer rows.Close()
	facts := tableVerificationFacts{}
	keys := map[string]struct{}{}
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return tableVerificationFacts{}, err
		}
		if convertSource {
			values, err = convertRow(sourceTable, tablePlan, columns, values)
			if err != nil {
				return tableVerificationFacts{}, err
			}
		}
		workspaceIndex := indexOf(columns, "workspace_id")
		if tablePlan.WorkspaceScoped && (workspaceIndex < 0 || strings.TrimSpace(fmt.Sprint(values[workspaceIndex])) == "") {
			facts.invalidWorkspaces++
		}
		row := canonicalRow(values)
		facts.digest = rollDigest(facts.digest, row)
		if facts.rows < 10 {
			facts.sampleDigest = rollDigest(facts.sampleDigest, row)
		}
		keyIndex := indexOf(columns, tablePlan.CheckpointKey[0])
		keys[string(canonicalRow([]any{values[keyIndex]}))] = struct{}{}
		facts.rows++
	}
	if err := rows.Err(); err != nil {
		return tableVerificationFacts{}, err
	}
	facts.keys = int64(len(keys))
	facts.duplicateRows = facts.rows - facts.keys
	facts.invalidReferences, err = countInvalidReferences(ctx, db, engine, schema, sourceTable)
	if err != nil {
		return tableVerificationFacts{}, err
	}
	return facts, nil
}

func countInvalidReferences(ctx context.Context, db *sql.DB, engine Engine, schema string, table TableInventory) (int64, error) {
	var invalid int64
	for _, foreign := range table.ForeignKeys {
		if len(foreign.Columns) == 0 || len(foreign.Columns) != len(foreign.ReferencedColumns) {
			return 0, fmt.Errorf("table %s has invalid foreign-key inventory", table.Name)
		}
		conditions := make([]string, 0, len(foreign.Columns))
		nonNull := make([]string, 0, len(foreign.Columns))
		for index := range foreign.Columns {
			child := verificationIdentifier(engine, foreign.Columns[index])
			parent := verificationIdentifier(engine, foreign.ReferencedColumns[index])
			conditions = append(conditions, "p."+parent+" = c."+child)
			nonNull = append(nonNull, "c."+child+" IS NOT NULL")
		}
		query := "SELECT COUNT(*) FROM " + verificationRelation(engine, schema, table.Name) + " c WHERE " + strings.Join(nonNull, " AND ") + " AND NOT EXISTS (SELECT 1 FROM " + verificationRelation(engine, schema, foreign.ReferencedTable) + " p WHERE " + strings.Join(conditions, " AND ") + ")"
		var count int64
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return 0, err
		}
		invalid += count
	}
	return invalid, nil
}

func verificationRelation(engine Engine, schema, table string) string {
	if engine == EnginePostgres && strings.TrimSpace(schema) != "" {
		return verificationIdentifier(engine, schema) + "." + verificationIdentifier(engine, table)
	}
	return verificationIdentifier(engine, table)
}

func verificationIdentifier(engine Engine, value string) string {
	if engine == EngineMySQL {
		return "`" + strings.ReplaceAll(value, "`", "``") + "`"
	}
	return quote(value)
}

func joinVerificationIdentifiers(engine Engine, values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = verificationIdentifier(engine, value)
	}
	return strings.Join(quoted, ", ")
}

type CutoverEvidence struct {
	Owner              string    `json:"owner"`
	RecordedAt         time.Time `json:"recorded_at"`
	SourceStopWrite    bool      `json:"source_stop_write"`
	WorkersDrained     bool      `json:"workers_drained"`
	SourceSnapshotID   string    `json:"source_snapshot_id"`
	FinalDeltaID       string    `json:"final_delta_id"`
	RollbackTarget     string    `json:"rollback_target"`
	SourceInventorySHA string    `json:"source_inventory_sha256"`
}

func ValidateCutoverEvidence(path string, now time.Time) (CutoverEvidence, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return CutoverEvidence{}, fmt.Errorf("final delta requires cutover evidence")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return CutoverEvidence{}, fmt.Errorf("read cutover evidence: %w", err)
	}
	var evidence CutoverEvidence
	if err := timevalue.UnmarshalJSON(raw, &evidence); err != nil {
		return CutoverEvidence{}, fmt.Errorf("parse cutover evidence: %w", err)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(evidence.Owner) == "" || strings.TrimSpace(evidence.SourceSnapshotID) == "" || strings.TrimSpace(evidence.FinalDeltaID) == "" || strings.TrimSpace(evidence.RollbackTarget) == "" || len(strings.TrimSpace(evidence.SourceInventorySHA)) != 64 {
		return CutoverEvidence{}, fmt.Errorf("cutover evidence identity is incomplete")
	}
	if !evidence.SourceStopWrite || !evidence.WorkersDrained {
		return CutoverEvidence{}, fmt.Errorf("cutover evidence must prove source stop-write and worker drain")
	}
	if evidence.RecordedAt.IsZero() || evidence.RecordedAt.After(now.Add(5*time.Minute)) || now.Sub(evidence.RecordedAt) > time.Hour {
		return CutoverEvidence{}, fmt.Errorf("cutover evidence is stale or invalid")
	}
	return evidence, nil
}
