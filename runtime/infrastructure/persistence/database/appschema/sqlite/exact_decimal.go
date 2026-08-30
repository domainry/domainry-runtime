package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	ormbuilder "github.com/domainry/domainry-orm/builder"
)

func (ApplicationSchemaStorageProfile) ExactDecimalCreateShadowTableSQL(renderer ormbuilder.Renderer, table, body, suffix string) string {
	return "CREATE TABLE " + renderer.Table(table) + " (" + body + ")" + suffix
}

func (ApplicationSchemaStorageProfile) ExactDecimalCopySQL(renderer ormbuilder.Renderer, target, source string, columns, expressions []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = renderer.Identifier(column)
	}
	return "INSERT INTO " + renderer.Table(target) + " (" + strings.Join(quoted, ", ") + ") SELECT " + strings.Join(expressions, ", ") + " FROM " + renderer.Table(source)
}

func (ApplicationSchemaStorageProfile) ExactDecimalDropTableSQL(renderer ormbuilder.Renderer, table string) string {
	return "DROP TABLE " + renderer.Table(table)
}

func (ApplicationSchemaStorageProfile) ExactDecimalRenameTableSQL(renderer ormbuilder.Renderer, from, to string) string {
	return "ALTER TABLE " + renderer.Table(from) + " RENAME TO " + renderer.Identifier(to)
}

type ExactDecimalQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type ExactDecimalExecutor interface {
	ExactDecimalQueryer
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (ApplicationSchemaStorageProfile) DisableExactDecimalForeignKeys(ctx context.Context, executor ExactDecimalExecutor) error {
	_, err := executor.ExecContext(ctx, "PRAGMA foreign_keys = OFF")
	return err
}

func (ApplicationSchemaStorageProfile) RestoreExactDecimalForeignKeys(ctx context.Context, executor ExactDecimalExecutor) error {
	_, err := executor.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	return err
}

func (ApplicationSchemaStorageProfile) VerifyExactDecimalForeignKeys(ctx context.Context, queryer ExactDecimalQueryer) error {
	rows, err := queryer.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("foreign key violation after exact decimal migration")
	}
	return rows.Err()
}

func (ApplicationSchemaStorageProfile) ExactDecimalEncodeExpression(renderer ormbuilder.Renderer, column string, precision, scale int, roundingMode string) string {
	return "runtime_exact_decimal_encode(" + renderer.Identifier(column) + ", " + fmt.Sprint(precision) + ", " + fmt.Sprint(scale) + ", '" + strings.ReplaceAll(roundingMode, "'", "''") + "')"
}

func (ApplicationSchemaStorageProfile) ExactDecimalWritableColumns(ctx context.Context, queryer ExactDecimalQueryer, table string) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, "PRAGMA table_xinfo(\""+strings.ReplaceAll(table, "\"", "\"\"")+"\")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			return nil, err
		}
		if hidden == 0 {
			columns = append(columns, name)
		}
	}
	return columns, rows.Err()
}

func (ApplicationSchemaStorageProfile) ExactDecimalSchemaArtifacts(ctx context.Context, queryer ExactDecimalQueryer, table string) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL ORDER BY type, name", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	artifacts := []string{}
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, statement)
	}
	return artifacts, rows.Err()
}

func (ApplicationSchemaStorageProfile) RewriteExactDecimalDDL(createSQL string, targets map[string]string) (string, string, error) {
	open, close := sqliteDDLBodyBounds(createSQL)
	if open < 0 || close <= open {
		return "", "", fmt.Errorf("unsupported CREATE TABLE definition")
	}
	parts := sqliteSplitTopLevel(createSQL[open+1 : close])
	seen := map[string]bool{}
	for index, part := range parts {
		key, nameEnd := sqliteLeadingIdentifier(part)
		target, exists := targets[key]
		if !exists {
			continue
		}
		typeEnd := sqliteColumnTypeEnd(part, nameEnd)
		remainder := strings.TrimLeftFunc(part[typeEnd:], unicode.IsSpace)
		parts[index] = part[:nameEnd] + " " + target
		if remainder != "" {
			parts[index] += " " + remainder
		}
		seen[key] = true
	}
	for key := range targets {
		if !seen[key] {
			return "", "", fmt.Errorf("column definition not found: %s", key)
		}
	}
	return strings.Join(parts, ","), createSQL[close+1:], nil
}

func sqliteDDLBodyBounds(value string) (int, int) {
	open := strings.Index(value, "(")
	if open < 0 {
		return -1, -1
	}
	depth, quote := 0, rune(0)
	for index, char := range value[open:] {
		absolute := open + index
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' || char == '`' || char == ']' {
			quote = char
			continue
		}
		switch char {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return open, absolute
			}
		}
	}
	return open, -1
}

func sqliteSplitTopLevel(value string) []string {
	parts, start, depth, quote := []string{}, 0, 0, rune(0)
	for index, char := range value {
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' || char == '`' || char == ']' {
			quote = char
			continue
		}
		if char == '(' {
			depth++
		} else if char == ')' {
			depth--
		} else if char == ',' && depth == 0 {
			parts = append(parts, value[start:index])
			start = index + 1
		}
	}
	return append(parts, value[start:])
}

func sqliteLeadingIdentifier(value string) (string, int) {
	start := 0
	for start < len(value) && unicode.IsSpace(rune(value[start])) {
		start++
	}
	if start >= len(value) {
		return "", start
	}
	if strings.ContainsRune("\"`[", rune(value[start])) {
		closing := byte(value[start])
		if closing == '[' {
			closing = ']'
		}
		end := strings.IndexByte(value[start+1:], closing)
		if end < 0 {
			return "", start
		}
		end += start + 1
		return value[start+1 : end], end + 1
	}
	end := start
	for end < len(value) && !unicode.IsSpace(rune(value[end])) {
		end++
	}
	return strings.Trim(value[start:end], "\"`[]"), end
}

func sqliteColumnTypeEnd(value string, start int) int {
	keywords := map[string]bool{"PRIMARY": true, "NOT": true, "UNIQUE": true, "CHECK": true, "DEFA" + "ULT": true, "COLLATE": true, "REFERENCES": true, "GENERATED": true, "AS": true}
	index := start
	for index < len(value) {
		for index < len(value) && unicode.IsSpace(rune(value[index])) {
			index++
		}
		wordStart := index
		for index < len(value) && (unicode.IsLetter(rune(value[index])) || value[index] == '_') {
			index++
		}
		if wordStart == index {
			index++
			continue
		}
		if keywords[strings.ToUpper(value[wordStart:index])] {
			return wordStart
		}
	}
	return len(value)
}
