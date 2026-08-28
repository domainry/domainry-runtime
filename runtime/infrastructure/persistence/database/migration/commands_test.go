package migration

import (
	"strings"
	"testing"
)

func TestDatabaseCommandsDoNotExposeMySQLPasswordInArguments(t *testing.T) {
	dsn := "runtime:top-secret@tcp(db.internal:3306)/runtime"
	for _, build := range []func(string, string, string) (CommandSpec, error){DatabaseBackupCommand, DatabaseRestoreCommand} {
		spec, err := build("mysql", dsn, "/secure/backup.sql")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(spec.Arguments, " "), "top-secret") || spec.Environment["MYSQL_PWD"] != "top-secret" {
			t.Fatalf("unsafe command spec: %+v", spec)
		}
	}
}

func TestPostgresRestoreUsesCleanControlledRestore(t *testing.T) {
	spec, err := DatabaseRestoreCommand("postgres", "postgres://runtime:secret@db/runtime", "/secure/backup.dump")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Arguments, " ")
	for _, required := range []string{"--clean", "--if-exists", "--no-owner", "--dbname"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("restore spec missing %s: %+v", required, spec)
		}
	}
}
