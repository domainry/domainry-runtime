package runtimehost

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/domainry/domainry-connector-sdk"
)

func TestConnectorTransportSMTPProbeAndDelivery(t *testing.T) {
	address, messages, stop := startConnectorSMTPFixture(t)
	defer stop()
	host, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	transport := newConnectorTransport().(connector.SMTPTransport)
	probe, err := transport.SendSMTP(t.Context(), connector.SMTPRequest{Host: host, Port: port, ProbeOnly: true})
	if err != nil || !probe.Connected {
		t.Fatalf("probe=%+v err=%v", probe, err)
	}

	address, messages, stop = startConnectorSMTPFixture(t)
	defer stop()
	host, portText, _ = net.SplitHostPort(address)
	port, _ = strconv.Atoi(portText)
	result, err := transport.SendSMTP(t.Context(), connector.SMTPRequest{Host: host, Port: port, EnvelopeFrom: "sender@example.test", Recipients: []string{"recipient@example.test"}, Message: []byte("Subject: test\r\n\r\nbody")})
	if err != nil || !result.Connected || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if message := <-messages; !strings.Contains(message, "body") {
		t.Fatalf("message=%q", message)
	}
}

func startConnectorSMTPFixture(t *testing.T) (string, <-chan string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	messages := make(chan string, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		reader, writer := bufio.NewReader(connection), bufio.NewWriter(connection)
		writeConnectorSMTP(writer, "220 fixture ESMTP")
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			command := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
				writeConnectorSMTP(writer, "250 fixture")
			case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
				writeConnectorSMTP(writer, "250 ok")
			case command == "DATA":
				writeConnectorSMTP(writer, "354 end with dot")
				var body strings.Builder
				for {
					line, readErr = reader.ReadString('\n')
					if readErr != nil || line == ".\r\n" {
						break
					}
					body.WriteString(line)
				}
				messages <- body.String()
				writeConnectorSMTP(writer, "250 queued")
			case command == "QUIT":
				writeConnectorSMTP(writer, "221 bye")
				return
			default:
				writeConnectorSMTP(writer, "250 ok")
			}
		}
	}()
	return listener.Addr().String(), messages, func() { _ = listener.Close() }
}

func writeConnectorSMTP(writer *bufio.Writer, line string) {
	_, _ = fmt.Fprintf(writer, "%s\r\n", line)
	_ = writer.Flush()
}

func TestConnectorTransportHTTPIsBoundedAndPreservesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.Header.Get("X-Request") != "one" {
			t.Fatalf("POST headers=%v", request.Header)
		}
		writer.Header().Add("X-Response", "first")
		writer.Header().Add("X-Response", "second")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("response"))
	}))
	defer server.Close()
	transport := newConnectorTransport()
	response, err := transport.RoundTripHTTP(t.Context(), connector.HTTPRequest{
		Method: http.MethodPost, URL: server.URL, Headers: map[string][]string{"X-Request": {"one"}}, Body: []byte("request"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated || string(response.Body) != "response" || !reflect.DeepEqual(response.Headers["X-Response"], []string{"first", "second"}) {
		t.Fatalf("response=%+v", response)
	}
	if _, err := transport.RoundTripHTTP(t.Context(), connector.HTTPRequest{Method: http.MethodGet, URL: server.URL, MaxResponseBytes: 3}); err == nil {
		t.Fatal("oversized Connector HTTP response was accepted")
	}
}

func TestConnectorTransportInjectsSecretQueryOnlyAtDispatch(t *testing.T) {
	const secret = "resolved-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("apiKey") != secret || request.URL.Query().Get("page") != "2" {
			t.Fatalf("query=%v", request.URL.Query())
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	originalURL := server.URL + "/resource?page=2"
	request := connector.HTTPRequest{
		Method:      http.MethodGet,
		URL:         originalURL,
		SecretQuery: map[string]string{"apiKey": secret},
	}
	if _, err := newConnectorTransport().RoundTripHTTP(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if request.URL != originalURL || strings.Contains(request.URL, secret) {
		t.Fatalf("input request URL was mutated or leaked a secret: %q", request.URL)
	}
}

func TestConnectorTransportRejectsUnsafeSecretQuery(t *testing.T) {
	transport := newConnectorTransport()
	tests := []connector.HTTPRequest{
		{Method: http.MethodGet, URL: "https://example.test/?apiKey=public", SecretQuery: map[string]string{"apiKey": "resolved-secret"}},
		{Method: http.MethodGet, URL: "https://example.test/", SecretQuery: map[string]string{" apiKey": "resolved-secret"}},
		{Method: http.MethodGet, URL: "https://example.test/", SecretQuery: map[string]string{"apiKey": ""}},
	}
	for _, request := range tests {
		_, err := transport.RoundTripHTTP(t.Context(), request)
		if err == nil {
			t.Fatalf("unsafe SecretQuery was accepted: %+v", request.SecretQuery)
		}
		if strings.Contains(err.Error(), "resolved-secret") {
			t.Fatalf("error leaked secret material: %v", err)
		}
	}
}

func TestConnectorTransportInjectsSecretHeadersAndFormOnlyAtDispatch(t *testing.T) {
	const secret = "refresh-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-secret" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		if err := request.ParseForm(); err != nil || request.Form.Get("grant_type") != "refresh_token" || request.Form.Get("refresh_token") != secret {
			t.Fatalf("form=%v error=%v", request.Form, err)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	request := connector.HTTPRequest{
		Method: http.MethodPost, URL: server.URL,
		Headers:       map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}},
		SecretHeaders: map[string][]string{"Authorization": {"Bearer access-secret"}},
		Body:          []byte("grant_type=refresh_token"),
		SecretForm:    map[string]string{"refresh_token": secret},
	}
	if _, err := newConnectorTransport().RoundTripHTTP(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(request.Body), secret) || len(request.Headers) != 1 {
		t.Fatalf("input request was mutated or leaked a secret: %+v", request)
	}
}

func TestConnectorTransportRejectsSecretHeaderAndFormCollisions(t *testing.T) {
	transport := newConnectorTransport()
	tests := []connector.HTTPRequest{
		{Method: http.MethodGet, URL: "https://example.test", Headers: map[string][]string{"Authorization": {"public"}}, SecretHeaders: map[string][]string{"Authorization": {"credential-value"}}},
		{Method: http.MethodGet, URL: "https://example.test", SecretHeaders: map[string][]string{"authorization": {"credential-value"}}},
		{Method: http.MethodPost, URL: "https://example.test", Headers: map[string][]string{"Content-Type": {"application/json"}}, SecretForm: map[string]string{"token": "credential-value"}},
		{Method: http.MethodPost, URL: "https://example.test", Headers: map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}}, Body: []byte("token=public"), SecretForm: map[string]string{"token": "credential-value"}},
	}
	for _, request := range tests {
		_, err := transport.RoundTripHTTP(t.Context(), request)
		if err == nil {
			t.Fatalf("unsafe request was accepted: %+v", request)
		}
		if strings.Contains(err.Error(), "credential-value") {
			t.Fatalf("error leaked secret material: %v", err)
		}
	}
}

func TestConnectorTransportInjectsSecretJSONOnlyAtDispatch(t *testing.T) {
	const secret = "project-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body["event"] != "activated" || body["api_key"] != secret {
			t.Fatalf("body=%v error=%v", body, err)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	request := connector.HTTPRequest{Method: http.MethodPost, URL: server.URL, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{"event":"activated"}`), SecretJSON: map[string]string{"api_key": secret}}
	if _, err := newConnectorTransport().RoundTripHTTP(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(request.Body), secret) {
		t.Fatalf("input request leaked secret: %+v", request)
	}
}

func TestConnectorTransportRejectsUnsafeSecretJSON(t *testing.T) {
	transport := newConnectorTransport()
	tests := []connector.HTTPRequest{
		{Method: http.MethodPost, URL: "https://example.test", Headers: map[string][]string{"Content-Type": {"text/plain"}}, Body: []byte(`{}`), SecretJSON: map[string]string{"api_key": "credential-value"}},
		{Method: http.MethodPost, URL: "https://example.test", Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`[]`), SecretJSON: map[string]string{"api_key": "credential-value"}},
		{Method: http.MethodPost, URL: "https://example.test", Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{"api_key":"public"}`), SecretJSON: map[string]string{"api_key": "credential-value"}},
		{Method: http.MethodPost, URL: "https://example.test", Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{}`), SecretJSON: map[string]string{" api_key": "credential-value"}},
	}
	for _, request := range tests {
		if _, err := transport.RoundTripHTTP(t.Context(), request); err == nil || strings.Contains(err.Error(), "credential-value") {
			t.Fatalf("unsafe request result err=%v request=%+v", err, request)
		}
	}
}

func TestConnectorTransportSQLHelpersBoundRowsAndKeepTypedValues(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(t.Context(), "CREATE TABLE sample (id INTEGER, name TEXT); INSERT INTO sample VALUES (1, 'one'), (2, 'two'), (3, 'three')"); err != nil {
		t.Fatal(err)
	}
	result, err := queryConnectorSQL(t.Context(), database, connector.SQLRequest{Statement: "SELECT id, name FROM sample ORDER BY id", MaxRows: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || !reflect.DeepEqual(result.Columns, []string{"id", "name"}) || len(result.Rows) != 2 || result.Rows[0][0] != int64(1) || result.Rows[0][1] != "one" {
		t.Fatalf("query result=%+v", result)
	}
	executed, err := execConnectorSQL(t.Context(), database, connector.SQLRequest{Statement: "UPDATE sample SET name = ? WHERE id = ?", Arguments: []any{"updated", 1}})
	if err != nil || executed.RowsAffected != 1 {
		t.Fatalf("exec result=%+v error=%v", executed, err)
	}
	transport := newConnectorTransport()
	if _, err := transport.ExecuteSQL(context.Background(), connector.SQLRequest{Driver: "sqlite", DSN: ":memory:", Operation: connector.SQLOperationPing}); err == nil {
		t.Fatal("project Connector gained local SQLite transport")
	}
}

func TestPrepareConnectorProvidersInjectsRuntimeTransport(t *testing.T) {
	options := Options{}
	called := false
	options.Connectors = func(transport connector.Transport) (connector.ProviderSet, error) {
		called = true
		if transport == nil {
			t.Fatal("Runtime supplied nil Connector transport")
		}
		return connector.ProviderSet{}, nil
	}
	registry, err := prepareConnectorProviders(options)
	if err != nil {
		t.Fatal(err)
	}
	if !called || registry == nil {
		t.Fatalf("called=%t registry=%v", called, registry)
	}
}
