package migration

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
)

type CommandSpec struct {
	Executable  string            `json:"executable"`
	Arguments   []string          `json:"arguments"`
	Environment map[string]string `json:"-"`
	StdinPath   string            `json:"stdin_path,omitempty"`
}

func DatabaseBackupCommand(engine, dsn, target string) (CommandSpec, error) {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "postgres":
		return CommandSpec{Executable: "pg_dump", Arguments: []string{"--format=custom", "--no-owner", "--file", target, dsn}}, nil
	case "mysql":
		cfg, err := mysqldriver.ParseDSN(dsn)
		if err != nil {
			return CommandSpec{}, fmt.Errorf("parse mysql DSN: %w", err)
		}
		args := mysqlConnectionArguments(cfg)
		args = append(args, "--single-transaction", "--routines", "--events", "--result-file="+target, cfg.DBName)
		return CommandSpec{Executable: "mysqldump", Arguments: args, Environment: map[string]string{"MYSQL_PWD": cfg.Passwd}}, nil
	default:
		return CommandSpec{}, fmt.Errorf("backup command is external only for postgres or mysql")
	}
}

func DatabaseRestoreCommand(engine, dsn, source string) (CommandSpec, error) {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "postgres":
		return CommandSpec{Executable: "pg_restore", Arguments: []string{"--clean", "--if-exists", "--no-owner", "--dbname", dsn, source}}, nil
	case "mysql":
		cfg, err := mysqldriver.ParseDSN(dsn)
		if err != nil {
			return CommandSpec{}, fmt.Errorf("parse mysql DSN: %w", err)
		}
		args := append(mysqlConnectionArguments(cfg), cfg.DBName)
		return CommandSpec{Executable: "mysql", Arguments: args, Environment: map[string]string{"MYSQL_PWD": cfg.Passwd}, StdinPath: source}, nil
	default:
		return CommandSpec{}, fmt.Errorf("restore command is external only for postgres or mysql")
	}
}

func mysqlConnectionArguments(cfg *mysqldriver.Config) []string {
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		host = cfg.Addr
	}
	args := []string{"--host", host, "--user", cfg.User}
	if port != "" {
		if _, err := strconv.Atoi(port); err == nil {
			args = append(args, "--port", port)
		}
	}
	if cfg.TLSConfig != "" {
		args = append(args, "--ssl-mode=VERIFY_IDENTITY")
	}
	return args
}
