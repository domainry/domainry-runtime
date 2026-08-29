package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
	mysqldriver "github.com/go-sql-driver/mysql"
)

func (Dialect) EnsureProjectDatabase(ctx context.Context, cfg config.Config) error {
	target, err := mysqldriver.ParseDSN(strings.TrimSpace(cfg.DatabaseDSN))
	if err != nil {
		return fmt.Errorf("parse MySQL project database DSN: %w", err)
	}
	databaseName := strings.TrimSpace(target.DBName)
	if databaseName == "" {
		return fmt.Errorf("MySQL project database name is required")
	}
	target.DBName = ""
	database, err := sql.Open("mysql", target.FormatDSN())
	if err != nil {
		return fmt.Errorf("open MySQL project database bootstrap connection: %w", err)
	}
	defer database.Close()
	quoted := "`" + strings.ReplaceAll(databaseName, "`", "``") + "`"
	if _, err := database.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+quoted); err != nil {
		return fmt.Errorf("create MySQL project database %q: %w", databaseName, err)
	}
	return nil
}
