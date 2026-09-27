package database

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	ormmigration "github.com/domainry/domainry-orm/migration"
	"vitess.io/vitess/go/vt/sqlparser"
)

var mysqlEmbeddedCreateIndexPattern = regexp.MustCompile(`(?is)'(CREATE\s+(?:UNIQUE\s+)?INDEX\s+[^']+)'`)
var mysqlDynamicIndexSetPattern = regexp.MustCompile(`(?is)^SET\s+(@[a-z0-9_]+)\s*=`)
var mysqlDynamicIndexPreparePattern = regexp.MustCompile(`(?is)^PREPARE\s+([a-z0-9_]+)\s+FROM\s+(@[a-z0-9_]+)\s*$`)
var mysqlDynamicIndexExecutePattern = regexp.MustCompile(`(?is)^EXECUTE\s+([a-z0-9_]+)\s*$`)
var mysqlDynamicIndexDeallocatePattern = regexp.MustCompile(`(?is)^DEALLOCATE\s+PREPARE\s+([a-z0-9_]+)\s*$`)

type mysqlMigrationStep struct {
	statement string
	table     *ormmigration.Table
	index     *mysqlMigrationIndex
}

type mysqlMigrationIndex struct {
	table string
	value ormmigration.Index
}

type mysqlMigrationContract struct {
	steps         []mysqlMigrationStep
	createdTables map[string]ormmigration.Table
	indexes       []mysqlMigrationIndex
}

func buildMySQLMigrationContract(statements []string) (mysqlMigrationContract, error) {
	contract := mysqlMigrationContract{createdTables: map[string]ormmigration.Table{}}
	parser, err := sqlparser.New(sqlparser.Options{})
	if err != nil {
		return mysqlMigrationContract{}, fmt.Errorf("create MySQL DDL parser: %w", err)
	}
	var dynamicVariable, dynamicPrepared string
	dynamicStage := 0
	for position, statement := range statements {
		statement = strings.TrimSpace(statement)
		parsed, err := parser.ParseStrictDDL(statement)
		if err != nil {
			return mysqlMigrationContract{}, fmt.Errorf("statement[%d] is not recoverable MySQL DDL: %w", position, err)
		}
		step := mysqlMigrationStep{statement: statement}
		switch value := parsed.(type) {
		case *sqlparser.CreateTable:
			table, err := mysqlTableContract(value)
			if err != nil {
				return mysqlMigrationContract{}, fmt.Errorf("statement[%d]: %w", position, err)
			}
			if _, exists := contract.createdTables[table.Name]; exists {
				return mysqlMigrationContract{}, fmt.Errorf("statement[%d] creates table %s more than once", position, table.Name)
			}
			contract.createdTables[table.Name] = table
			step.table = &table
		case *sqlparser.AlterTable:
			index, err := mysqlIndexContract(value)
			if err != nil {
				return mysqlMigrationContract{}, fmt.Errorf("statement[%d]: %w", position, err)
			}
			contract.indexes = append(contract.indexes, index)
			step.index = &index
		case *sqlparser.Set:
			set := mysqlDynamicIndexSetPattern.FindStringSubmatch(statement)
			matches := mysqlEmbeddedCreateIndexPattern.FindStringSubmatch(statement)
			if dynamicStage != 0 || len(set) != 2 || len(matches) != 2 {
				return mysqlMigrationContract{}, fmt.Errorf("statement[%d] has no recoverable index postcondition", position)
			}
			dynamicVariable, dynamicStage = strings.ToLower(set[1]), 1
			nested, err := parser.ParseStrictDDL(matches[1])
			if err != nil {
				return mysqlMigrationContract{}, fmt.Errorf("statement[%d] embedded index is invalid: %w", position, err)
			}
			alter, ok := nested.(*sqlparser.AlterTable)
			if !ok {
				return mysqlMigrationContract{}, fmt.Errorf("statement[%d] embedded statement is not an index", position)
			}
			index, err := mysqlIndexContract(alter)
			if err != nil {
				return mysqlMigrationContract{}, fmt.Errorf("statement[%d] embedded index: %w", position, err)
			}
			contract.indexes = append(contract.indexes, index)
		case *sqlparser.PrepareStmt:
			prepare := mysqlDynamicIndexPreparePattern.FindStringSubmatch(statement)
			if dynamicStage != 1 || len(prepare) != 3 || strings.ToLower(prepare[2]) != dynamicVariable {
				return mysqlMigrationContract{}, fmt.Errorf("statement[%d] is not the PREPARE step for the guarded index", position)
			}
			dynamicPrepared, dynamicStage = strings.ToLower(prepare[1]), 2
		case *sqlparser.ExecuteStmt:
			execute := mysqlDynamicIndexExecutePattern.FindStringSubmatch(statement)
			if dynamicStage != 2 || len(execute) != 2 || strings.ToLower(execute[1]) != dynamicPrepared {
				return mysqlMigrationContract{}, fmt.Errorf("statement[%d] is not the EXECUTE step for the guarded index", position)
			}
			dynamicStage = 3
		case *sqlparser.DeallocateStmt:
			deallocate := mysqlDynamicIndexDeallocatePattern.FindStringSubmatch(statement)
			if dynamicStage != 3 || len(deallocate) != 2 || strings.ToLower(deallocate[1]) != dynamicPrepared {
				return mysqlMigrationContract{}, fmt.Errorf("statement[%d] is not the DEALLOCATE step for the guarded index", position)
			}
			dynamicStage = 4
		default:
			return mysqlMigrationContract{}, fmt.Errorf("statement[%d] type %T is not recoverable MySQL DDL", position, parsed)
		}
		contract.steps = append(contract.steps, step)
	}
	if dynamicStage != 0 && dynamicStage != 4 {
		return mysqlMigrationContract{}, fmt.Errorf("guarded dynamic-index sequence is incomplete")
	}
	if len(contract.createdTables)+len(contract.indexes) == 0 {
		return mysqlMigrationContract{}, fmt.Errorf("migration has no physical MySQL postcondition")
	}
	for _, index := range contract.indexes {
		if table, exists := contract.createdTables[index.table]; exists {
			table.Indexes = append(table.Indexes, index.value)
			contract.createdTables[index.table] = table
		}
	}
	return contract, nil
}

func mysqlTableContract(statement *sqlparser.CreateTable) (ormmigration.Table, error) {
	if statement == nil || statement.TableSpec == nil {
		return ormmigration.Table{}, fmt.Errorf("CREATE TABLE has no table definition")
	}
	table := ormmigration.Table{Name: statement.Table.Name.String()}
	if !moduleSchemaIdentityPattern.MatchString(table.Name) {
		return ormmigration.Table{}, fmt.Errorf("CREATE TABLE name %q is invalid", table.Name)
	}
	if len(statement.TableSpec.Constraints) != 0 {
		return ormmigration.Table{}, fmt.Errorf("CREATE TABLE %s contains constraints that the recovery verifier cannot inspect", table.Name)
	}
	primary := map[string]bool{}
	indexNames := map[string]int{}
	for _, definition := range statement.TableSpec.Indexes {
		if definition.Info.Type == sqlparser.IndexTypePrimary {
			columns, err := mysqlIndexColumns(definition)
			if err != nil {
				return ormmigration.Table{}, fmt.Errorf("table %s primary key: %w", table.Name, err)
			}
			for _, column := range columns {
				primary[column] = true
			}
			continue
		}
		index, err := mysqlIndexDefinition(definition)
		if err != nil {
			return ormmigration.Table{}, fmt.Errorf("table %s: %w", table.Name, err)
		}
		index.Name = nextMySQLIndexName(index.Name, indexNames)
		table.Indexes = append(table.Indexes, index)
	}
	for _, definition := range statement.TableSpec.Columns {
		if definition == nil || definition.Type == nil {
			return ormmigration.Table{}, fmt.Errorf("table %s contains an invalid column", table.Name)
		}
		name := definition.Name.String()
		columnType := *definition.Type
		columnType.Options = nil
		columnType.Charset = sqlparser.ColumnCharset{}
		nullable := true
		if definition.Type.Options != nil && definition.Type.Options.Null != nil {
			nullable = *definition.Type.Options.Null
		}
		primaryKey := primary[name] || definition.Type.Options != nil && definition.Type.Options.KeyOpt == sqlparser.ColKeyPrimary
		if primaryKey {
			nullable = false
		}
		table.Columns = append(table.Columns, ormmigration.Column{Name: name, Type: sqlparser.String(&columnType), Nullable: nullable, PrimaryKey: primaryKey})
		if definition.Type.Options != nil {
			switch definition.Type.Options.KeyOpt {
			case sqlparser.ColKeyUnique, sqlparser.ColKeyUniqueKey:
				table.Indexes = append(table.Indexes, ormmigration.Index{Name: nextMySQLIndexName(name, indexNames), Unique: true, Columns: []string{name}})
			}
		}
	}
	if len(table.Columns) == 0 {
		return ormmigration.Table{}, fmt.Errorf("CREATE TABLE %s has no columns", table.Name)
	}
	return table, nil
}

func nextMySQLIndexName(base string, used map[string]int) string {
	used[base]++
	if used[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, used[base])
}

func mysqlIndexContract(statement *sqlparser.AlterTable) (mysqlMigrationIndex, error) {
	if statement == nil || len(statement.AlterOptions) != 1 {
		return mysqlMigrationIndex{}, fmt.Errorf("only one CREATE INDEX effect per statement is supported")
	}
	add, ok := statement.AlterOptions[0].(*sqlparser.AddIndexDefinition)
	if !ok {
		return mysqlMigrationIndex{}, fmt.Errorf("ALTER TABLE effect %T is not recoverable", statement.AlterOptions[0])
	}
	index, err := mysqlIndexDefinition(add.IndexDefinition)
	if err != nil {
		return mysqlMigrationIndex{}, err
	}
	table := statement.Table.Name.String()
	if !moduleSchemaIdentityPattern.MatchString(table) {
		return mysqlMigrationIndex{}, fmt.Errorf("index table name %q is invalid", table)
	}
	return mysqlMigrationIndex{table: table, value: index}, nil
}

func mysqlIndexDefinition(definition *sqlparser.IndexDefinition) (ormmigration.Index, error) {
	if definition == nil || definition.Info == nil || definition.Info.Type == sqlparser.IndexTypePrimary {
		return ormmigration.Index{}, fmt.Errorf("index definition is invalid")
	}
	columns, err := mysqlIndexColumns(definition)
	if err != nil {
		return ormmigration.Index{}, err
	}
	name := definition.Info.Name.String()
	if name == "" {
		name = columns[0]
	}
	if !moduleSchemaIdentityPattern.MatchString(name) {
		return ormmigration.Index{}, fmt.Errorf("index name %q is invalid", name)
	}
	return ormmigration.Index{Name: name, Unique: definition.Info.Type == sqlparser.IndexTypeUnique, Columns: columns}, nil
}

func mysqlIndexColumns(definition *sqlparser.IndexDefinition) ([]string, error) {
	columns := make([]string, len(definition.Columns))
	for position, column := range definition.Columns {
		if column == nil || column.Column.IsEmpty() || column.Expression != nil || column.Length != nil {
			return nil, fmt.Errorf("column[%d] is an expression or prefix index", position)
		}
		columns[position] = column.Column.String()
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("index has no columns")
	}
	return columns, nil
}

func (s *RuntimeStore) reconcileMySQLMigration(ctx context.Context, contract mysqlMigrationContract) error {
	for _, step := range contract.steps {
		switch {
		case step.table != nil:
			actual, exists, err := s.inspectModuleSchemaTable(ctx, step.table.Name)
			if err != nil {
				return err
			}
			if exists {
				if err := compareModuleSchemaTableSubset(*step.table, actual); err != nil {
					return fmt.Errorf("table %s: %w", step.table.Name, err)
				}
				continue
			}
		case step.index != nil:
			present, err := s.proveMySQLIndex(ctx, *step.index)
			if err != nil {
				return err
			}
			if present {
				continue
			}
		}
		statement := s.runtimeColumnDefinition(step.statement)
		if _, err := s.schemaDatabase().ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	complete, err := s.proveMySQLMigrationContract(ctx, contract)
	if err != nil {
		return err
	}
	if !complete {
		return fmt.Errorf("migration physical postcondition is incomplete")
	}
	return nil
}

func (s *RuntimeStore) proveMySQLMigrationContract(ctx context.Context, contract mysqlMigrationContract) (bool, error) {
	present, total := 0, len(contract.createdTables)
	for _, expected := range contract.createdTables {
		actual, exists, err := s.inspectModuleSchemaTable(ctx, expected.Name)
		if err != nil {
			return false, err
		}
		if !exists {
			continue
		}
		if err := compareModuleSchemaTable(expected, actual); err != nil {
			return false, fmt.Errorf("table %s: %w", expected.Name, err)
		}
		present++
	}
	for _, expected := range contract.indexes {
		if _, created := contract.createdTables[expected.table]; created {
			continue
		}
		total++
		exists, err := s.proveMySQLIndex(ctx, expected)
		if err != nil {
			return false, err
		}
		if exists {
			present++
		}
	}
	if total == 0 {
		return false, fmt.Errorf("migration has no physical MySQL postcondition")
	}
	if present == 0 {
		return false, nil
	}
	if present != total {
		return false, fmt.Errorf("partial migration schema: found %d of %d physical effects", present, total)
	}
	return true, nil
}

func (s *RuntimeStore) proveMySQLIndex(ctx context.Context, expected mysqlMigrationIndex) (bool, error) {
	actual, exists, err := s.inspectModuleSchemaTable(ctx, expected.table)
	if err != nil || !exists {
		return false, err
	}
	for _, index := range actual.indexes {
		if index.name != expected.value.Name {
			continue
		}
		if index.unique != expected.value.Unique || !equalModuleColumns(index.columns, expected.value.Columns) {
			return false, fmt.Errorf("index %s on %s has unique=%t columns=%v, want unique=%t columns=%v", index.name, expected.table, index.unique, index.columns, expected.value.Unique, expected.value.Columns)
		}
		return true, nil
	}
	return false, nil
}
