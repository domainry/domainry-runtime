package config

import (
	"strings"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
)

func TestLoadContractUsesPlatformDatabaseURL(t *testing.T) {
	url := "mysql://app-user:p%40ssword@gateway.example.test:4000/product_db?ssl=%7B%22minVersion%22%3A%22TLSv1.2%22%2C%22rejectUnauthorized%22%3Atrue%7D"
	cfg, snapshot, err := LoadContract(Source{Name: "environment", Priority: 300, Values: map[string]string{"DATABASE_URL": url}})
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := mysqldriver.ParseDSN(cfg.DatabaseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseDriver != "mysql" || cfg.DatabaseURL != "" || dsn.User != "app-user" || dsn.Passwd != "p@ssword" || dsn.Addr != "gateway.example.test:4000" || dsn.DBName != "product_db" || dsn.TLSConfig != "true" || !dsn.ParseTime {
		t.Fatal("platform URL did not produce a verified MySQL Runtime configuration")
	}
	if entry := snapshot.Entries["DATABASE_URL"]; !entry.Redacted || entry.Version != "unversioned" {
		t.Fatalf("DATABASE_URL provenance is not redacted: %+v", entry)
	}
}

func TestLoadContractRejectsUnsafePlatformDatabaseURLWithoutLeakingPassword(t *testing.T) {
	for _, url := range []string{
		"mysql://app:secret@example.test:4000/db?ssl=false",
		"mysql://app:secret@example.test:4000/db?ssl=%7B%22rejectUnauthorized%22%3Afalse%7D",
		"mysql://app:secret@example.test:4000/db?ssl=true&unknown=yes",
		"mysql://app:secret@example.test/db",
		"postgres://app:secret@example.test:5432/db",
	} {
		_, _, err := LoadContract(Source{Name: "environment", Values: map[string]string{"DATABASE_URL": url}})
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe DATABASE_URL error = %v", err)
		}
	}
	_, _, err := LoadContract(Source{Name: "environment", Values: map[string]string{
		"DATABASE_URL": "mysql://app:secret@example.test:4000/db", "DATABASE_DSN": "other",
	}})
	if err == nil || !strings.Contains(err.Error(), "cannot both be set") {
		t.Fatalf("conflicting database settings error = %v", err)
	}
}
