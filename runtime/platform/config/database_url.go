package config

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// resolveDatabaseURL maps the hosting platform's secret URL to the Runtime's
// native MySQL DSN. It deliberately fails closed instead of falling back to
// the local SQLite default when a cloud database URL is malformed.
func (c *Config) resolveDatabaseURL() error {
	raw := strings.TrimSpace(c.DatabaseURL)
	if raw == "" {
		return nil
	}
	if strings.TrimSpace(c.DatabaseDSN) != "" {
		return errors.New("DATABASE_URL and DATABASE_DSN cannot both be set")
	}
	if driver := strings.ToLower(strings.TrimSpace(c.DatabaseDriver)); driver != "" && driver != "sqlite" && driver != "mysql" {
		return errors.New("DATABASE_URL conflicts with DATABASE_DRIVER")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "mysql" || parsed.Opaque != "" || parsed.Fragment != "" || parsed.User == nil {
		return errors.New("DATABASE_URL must be a MySQL URL")
	}
	user := parsed.User.Username()
	password, hasPassword := parsed.User.Password()
	host := parsed.Hostname()
	port := parsed.Port()
	database := strings.TrimPrefix(parsed.Path, "/")
	if user == "" || !hasPassword || host == "" || port == "" || database == "" || strings.Contains(database, "/") {
		return errors.New("DATABASE_URL must include user, password, host, port, and database")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("DATABASE_URL has an invalid port")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return errors.New("DATABASE_URL has an invalid query")
	}
	for key := range query {
		if key != "ssl" {
			return errors.New("DATABASE_URL has an unsupported query parameter")
		}
	}
	if len(query["ssl"]) > 1 {
		return errors.New("DATABASE_URL has multiple SSL settings")
	}
	if ssl := query.Get("ssl"); ssl != "" && ssl != "true" {
		var settings struct {
			MinVersion         string `json:"minVersion"`
			RejectUnauthorized bool   `json:"rejectUnauthorized"`
		}
		if json.Unmarshal([]byte(ssl), &settings) != nil || settings.MinVersion != "TLSv1.2" || !settings.RejectUnauthorized {
			return errors.New("DATABASE_URL requires verified TLS 1.2 or newer")
		}
	}
	address := net.JoinHostPort(host, port)
	dsn := (&mysqldriver.Config{
		User: user, Passwd: password, Net: "tcp", Addr: address, DBName: database,
		ParseTime: true, TLSConfig: "true",
	}).FormatDSN()
	c.DatabaseDriver = "mysql"
	c.DatabaseDSN = dsn
	c.DatabaseURL = ""
	return nil
}
