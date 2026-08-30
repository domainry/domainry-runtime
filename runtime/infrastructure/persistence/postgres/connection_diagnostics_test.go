package postgres

import (
	"context"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresSafeStatusContainsOnlyOperationalFields(t *testing.T) {
	profile := ConnectionProfile{Backend: BackendPostgres, Mode: ConnectionModeSessionPooler, Schema: "runtime", MaxOpenConns: 12, MaxIdleConns: 4, ServerMaxConns: 100, ReservedConns: 10, RuntimeReplicaCount: 3, TLS: true, TLSVerified: true, PreparedStatements: true, MigrationConfigured: true, MigrationMode: "verify"}
	status := profile.SafeStatus()
	if status.Backend != BackendPostgres || status.Mode != ConnectionModeSessionPooler || status.Schema != "runtime" || status.MaxOpenConns != 12 || status.MaxIdleConns != 4 || status.ServerMaxConns != 100 || status.ReservedConns != 10 || status.RuntimeReplicaCount != 3 || !status.TLS || !status.TLSVerified || !status.PreparedStatements || !status.MigrationConfigured || status.MigrationMode != "verify" {
		t.Fatalf("safe status=%#v", status)
	}
	if _, found := reflect.TypeOf(status).FieldByName("DSN"); found {
		t.Fatal("safe status must not expose a DSN")
	}
}

func TestPostgresCapabilityProbe(t *testing.T) {
	profile := ConnectionProfile{Schema: "runtime"}
	if _, err := profile.Probe(t.Context(), nil); err == nil {
		t.Fatal("expected nil database error")
	}
	want := Capabilities{ServerVersion: "16.2", Database: "runtime", User: "runtime_user", CurrentSchema: "runtime", TLS: true, ReadOnly: false, InRecovery: false, SchemaExists: true, SchemaUsage: true, SchemaCreate: false, AdvisoryLocks: true}
	query := ""
	db := sql.OpenDB(diagnosticConnector{values: []driver.Value{want.ServerVersion, want.Database, want.User, want.CurrentSchema, want.TLS, want.ReadOnly, want.InRecovery, want.SchemaExists, want.SchemaUsage, want.SchemaCreate, want.AdvisoryLocks}, query: &query})
	defer db.Close()
	got, err := profile.Probe(t.Context(), db)
	if err != nil || got != want {
		t.Fatalf("capabilities=%#v err=%v", got, err)
	}
	if !strings.Contains(query, "current_setting('default_transaction_read_only')") || strings.Contains(query, "current_setting('transaction_read_only')") {
		t.Fatalf("probe must inspect the session default instead of its own read-only query transaction: %s", query)
	}
	got, err = profile.ProbeWithBackoff(t.Context(), db)
	if err != nil || got != want {
		t.Fatalf("backoff capabilities=%#v err=%v", got, err)
	}
	failure := errors.New("catalog unavailable")
	errorDB := sql.OpenDB(diagnosticConnector{err: failure})
	defer errorDB.Close()
	if _, err := profile.Probe(t.Context(), errorDB); !errors.Is(err, failure) {
		t.Fatalf("probe error=%v", err)
	}
}

func TestPostgresProbePoolBackoffContracts(t *testing.T) {
	poolErr := &pgconn.PgError{Code: "53300", Message: "too many connections"}
	t.Run("normalized defaults and eventual success", func(t *testing.T) {
		calls := 0
		delays := []time.Duration{}
		capability, err := probeWithPoolBackoff(t.Context(), 0, 0, func(context.Context) (Capabilities, error) {
			calls++
			return Capabilities{Database: "runtime"}, nil
		}, func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		})
		if err != nil || capability.Database != "runtime" || calls != 1 || len(delays) != 0 {
			t.Fatalf("attempt normalization capability=%#v calls=%d delays=%v err=%v", capability, calls, delays, err)
		}

		calls = 0
		capability, err = probeWithPoolBackoff(t.Context(), 3, 0, func(context.Context) (Capabilities, error) {
			calls++
			if calls < 3 {
				return Capabilities{}, poolErr
			}
			return Capabilities{Database: "runtime"}, nil
		}, func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		})
		if err != nil || capability.Database != "runtime" || calls != 3 || !reflect.DeepEqual(delays, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}) {
			t.Fatalf("capability=%#v calls=%d delays=%v err=%v", capability, calls, delays, err)
		}
	})

	t.Run("failure classification wait and cap", func(t *testing.T) {
		plain := errors.New("authentication rejected")
		calls := 0
		if _, err := probeWithPoolBackoff(t.Context(), 3, time.Millisecond, func(context.Context) (Capabilities, error) { calls++; return Capabilities{}, plain }, func(context.Context, time.Duration) error { t.Fatal("unexpected wait"); return nil }); !errors.Is(err, plain) || calls != 1 {
			t.Fatalf("plain error=%v calls=%d", err, calls)
		}
		waitErr := context.Canceled
		if _, err := probeWithPoolBackoff(t.Context(), 3, time.Second, func(context.Context) (Capabilities, error) { return Capabilities{}, poolErr }, func(_ context.Context, delay time.Duration) error {
			if delay != time.Second {
				t.Fatalf("capped delay=%s", delay)
			}
			return waitErr
		}); !errors.Is(err, waitErr) {
			t.Fatalf("wait error=%v", err)
		}
		if _, err := probeWithPoolBackoff(t.Context(), 2, time.Second, func(context.Context) (Capabilities, error) { return Capabilities{}, poolErr }, func(context.Context, time.Duration) error { return nil }); !errors.Is(err, poolErr) {
			t.Fatalf("terminal pool error=%v", err)
		}
		waits := 0
		if _, err := probeWithPoolBackoff(t.Context(), 3, 600*time.Millisecond, func(context.Context) (Capabilities, error) { return Capabilities{}, poolErr }, func(_ context.Context, delay time.Duration) error {
			waits++
			if waits == 1 && delay != 600*time.Millisecond || waits == 2 && delay != time.Second {
				t.Fatalf("delay[%d]=%s", waits, delay)
			}
			if waits == 2 {
				return waitErr
			}
			return nil
		}); !errors.Is(err, waitErr) || waits != 2 {
			t.Fatalf("capped retry waits=%d err=%v", waits, err)
		}
	})

	if got := (ConnectionProfile{}).InitialPoolRetryBackoff(); got != 100*time.Millisecond {
		t.Fatalf("initial backoff=%s", got)
	}
}

func TestWaitForPoolRetry(t *testing.T) {
	if err := waitForPoolRetry(t.Context(), time.Millisecond); err != nil {
		t.Fatalf("timer wait: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitForPoolRetry(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait=%v", err)
	}
}

func TestPostgresRuntimeCapabilityValidation(t *testing.T) {
	base := Capabilities{Database: "runtime", TLS: true}
	tests := []struct {
		name     string
		profile  ConnectionProfile
		query    Capabilities
		migrator Capabilities
		kind     string
	}{
		{name: "missing query database", query: Capabilities{}, kind: FailureSchemaIncompatible},
		{name: "query read only", query: Capabilities{Database: "runtime", ReadOnly: true}, kind: "read_only"},
		{name: "query recovery", query: Capabilities{Database: "runtime", InRecovery: true}, kind: "read_only"},
		{name: "query tls", profile: ConnectionProfile{TLS: true}, query: Capabilities{Database: "runtime"}, kind: FailureTLS},
		{name: "missing migrator", profile: ConnectionProfile{MigrationConfigured: true}, query: base, kind: FailureSchemaIncompatible},
		{name: "different migrator database", profile: ConnectionProfile{MigrationConfigured: true}, query: base, migrator: Capabilities{Database: "other"}, kind: FailureSchemaIncompatible},
		{name: "migrator read only", profile: ConnectionProfile{MigrationConfigured: true}, query: base, migrator: Capabilities{Database: "runtime", ReadOnly: true}, kind: "read_only"},
		{name: "migrator recovery", profile: ConnectionProfile{MigrationConfigured: true}, query: base, migrator: Capabilities{Database: "runtime", InRecovery: true}, kind: "read_only"},
		{name: "migrator tls", profile: ConnectionProfile{MigrationConfigured: true, TLS: true}, query: base, migrator: Capabilities{Database: "runtime"}, kind: FailureTLS},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.profile.ValidateRuntimeCapabilities(test.query, test.migrator)
			var capabilityErr CapabilityError
			if !errors.As(err, &capabilityErr) || capabilityErr.Kind != test.kind || capabilityErr.Error() == "" {
				t.Fatalf("error=%v kind=%q", err, capabilityErr.Kind)
			}
		})
	}
	if err := (ConnectionProfile{MigrationConfigured: true, TLS: true}).ValidateRuntimeCapabilities(base, base); err != nil {
		t.Fatalf("valid capabilities: %v", err)
	}
	if err := (ConnectionProfile{MigrationConfigured: true}).ValidateRuntimeCapabilities(base, Capabilities{Database: "runtime"}); err != nil {
		t.Fatalf("valid non-TLS migrator capabilities: %v", err)
	}
}

func TestClassifyPostgresConnectionFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", want: ""},
		{name: "dns", err: &net.DNSError{Err: "no such host", Name: "db.test"}, want: FailureDNS},
		{name: "certificate", err: x509.UnknownAuthorityError{Cert: &x509.Certificate{}}, want: FailureTLS},
		{name: "ipv4", err: &net.OpError{Addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.1")}, Err: errors.New("down")}, want: FailureNetworkIPv4},
		{name: "ipv6", err: &net.OpError{Addr: &net.TCPAddr{IP: net.ParseIP("2001:db8::1")}, Err: errors.New("down")}, want: FailureNetworkIPv6},
		{name: "non tcp network address", err: &net.OpError{Addr: &net.IPAddr{IP: net.ParseIP("192.0.2.1")}, Err: errors.New("down")}, want: FailureUnknown},
		{name: "nil tcp ip", err: &net.OpError{Addr: &net.TCPAddr{}, Err: errors.New("down")}, want: FailureUnknown},
		{name: "authentication", err: &pgconn.PgError{Code: "28P01"}, want: FailureAuthentication},
		{name: "authorization authentication", err: &pgconn.PgError{Code: "28000"}, want: FailureAuthentication},
		{name: "pool", err: &pgconn.PgError{Code: "53300"}, want: FailurePoolExhausted},
		{name: "program limit pool", err: &pgconn.PgError{Code: "53400"}, want: FailurePoolExhausted},
		{name: "database missing", err: &pgconn.PgError{Code: "3D000"}, want: FailureSchemaIncompatible},
		{name: "schema missing", err: &pgconn.PgError{Code: "3F000"}, want: FailureSchemaIncompatible},
		{name: "table missing", err: &pgconn.PgError{Code: "42P01"}, want: FailureSchemaIncompatible},
		{name: "shutdown", err: &pgconn.PgError{Code: "57P01"}, want: FailureServerUnavailable},
		{name: "crash", err: &pgconn.PgError{Code: "57P02"}, want: FailureServerUnavailable},
		{name: "cannot connect", err: &pgconn.PgError{Code: "57P03"}, want: FailureServerUnavailable},
		{name: "message certificate", err: errors.New("certificate rejected"), want: FailureTLS},
		{name: "message tls", err: errors.New("TLS handshake failed"), want: FailureTLS},
		{name: "message ssl", err: errors.New("SSL failure"), want: FailureTLS},
		{name: "message network unreachable", err: errors.New("network is unreachable"), want: FailureNetworkIPv4},
		{name: "message route", err: errors.New("no route to host"), want: FailureNetworkIPv4},
		{name: "message server unavailable", err: errors.New("server is unavailable"), want: FailureServerUnavailable},
		{name: "message unavailable", err: errors.New("database is unavailable"), want: FailureServerUnavailable},
		{name: "message too many connections", err: errors.New("too many connections"), want: FailurePoolExhausted},
		{name: "message max clients", err: errors.New("max client connections reached"), want: FailurePoolExhausted},
		{name: "unknown postgres", err: &pgconn.PgError{Code: "XX000"}, want: FailureUnknown},
		{name: "unknown", err: errors.New("unexpected"), want: FailureUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyConnectionFailure(test.err); got != test.want {
				t.Fatalf("classification=%q want=%q err=%v", got, test.want, test.err)
			}
		})
	}
}

type diagnosticConnector struct {
	values []driver.Value
	err    error
	query  *string
}

func (c diagnosticConnector) Connect(context.Context) (driver.Conn, error) {
	return diagnosticConn(c), nil
}
func (diagnosticConnector) Driver() driver.Driver { return diagnosticDriver{} }

type diagnosticDriver struct{}

func (diagnosticDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type diagnosticConn diagnosticConnector

func (diagnosticConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (diagnosticConn) Close() error                        { return nil }
func (diagnosticConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (c diagnosticConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.query != nil {
		*c.query = query
	}
	if c.err != nil {
		return nil, c.err
	}
	return &diagnosticRows{values: c.values}, nil
}

type diagnosticRows struct {
	values []driver.Value
	done   bool
}

func (diagnosticRows) Columns() []string {
	return []string{"version", "database", "user", "schema", "tls", "read_only", "recovery", "schema_exists", "schema_usage", "schema_create", "advisory_locks"}
}
func (diagnosticRows) Close() error { return nil }
func (r *diagnosticRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	copy(values, r.values)
	r.done = true
	return nil
}
