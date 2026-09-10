package runtimehost

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/safehttp"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"

	"github.com/domainry/domainry-connector-sdk"
)

const (
	defaultConnectorHTTPResponseBytes = int64(1 << 20)
	maxConnectorHTTPResponseBytes     = int64(8 << 20)
	defaultConnectorSQLRows           = 100
	maxConnectorSQLRows               = 1000
)

type connectorTransport struct {
	httpClient    *http.Client
	processPolicy ConnectorProcessPolicy
}

func newConnectorTransport() connector.Transport {
	return newConnectorTransportWithProcessPolicy(ConnectorProcessPolicy{})
}

func newConnectorTransportWithProcessPolicy(policy ConnectorProcessPolicy) connector.Transport {
	return &connectorTransport{
		httpClient:    safehttp.NewClient(safehttp.Policy{AllowLiteralLoopback: true}),
		processPolicy: policy,
	}
}

func (t *connectorTransport) SendSMTP(ctx context.Context, request connector.SMTPRequest) (result connector.SMTPResult, returnErr error) {
	host := strings.TrimSpace(request.Host)
	if host == "" || request.Port < 1 || request.Port > 65535 {
		return result, errors.New("Connector SMTP host and valid port are required")
	}
	if request.ImplicitTLS && request.StartTLS {
		return result, errors.New("Connector SMTP implicit TLS and STARTTLS are mutually exclusive")
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tlsConfig, err := connectorSMTPTLSConfig(host, request)
	if err != nil {
		return result, err
	}
	dialer := &net.Dialer{Timeout: timeout}
	address := net.JoinHostPort(host, strconv.Itoa(request.Port))
	var connection net.Conn
	if request.ImplicitTLS {
		connection, err = tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return result, err
	}
	defer connection.Close()
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return result, err
	}
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		return result, err
	}
	defer func() {
		if closeErr := client.Close(); returnErr == nil && closeErr != nil {
			returnErr = closeErr
		}
	}()
	if request.StartTLS {
		if err := client.StartTLS(tlsConfig); err != nil {
			return result, err
		}
	}
	result.Connected = true
	if request.ProbeOnly {
		return result, nil
	}
	if strings.TrimSpace(request.EnvelopeFrom) == "" || len(request.Recipients) == 0 || len(request.Message) == 0 {
		return result, errors.New("Connector SMTP envelope and message are required")
	}
	if request.Username != "" {
		if request.SecretPassword == "" {
			return result, errors.New("Connector SMTP password is required when username is configured")
		}
		if err := client.Auth(smtp.PlainAuth("", request.Username, request.SecretPassword, host)); err != nil {
			return result, err
		}
	}
	if err := client.Mail(request.EnvelopeFrom); err != nil {
		return result, err
	}
	for _, recipient := range request.Recipients {
		if err := client.Rcpt(recipient); err != nil {
			return result, err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return result, err
	}
	if _, err := writer.Write(request.Message); err != nil {
		_ = writer.Close()
		return result, err
	}
	if err := writer.Close(); err != nil {
		return result, err
	}
	result.Accepted = true
	return result, nil
}

func connectorSMTPTLSConfig(host string, request connector.SMTPRequest) (*tls.Config, error) {
	serverName := strings.TrimSpace(request.TLSServerName)
	if serverName == "" {
		serverName = host
	}
	config := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(request.TLSCAPEM) == "" {
		return config, nil
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM([]byte(request.TLSCAPEM)) {
		return nil, errors.New("Connector SMTP TLS CA PEM is invalid")
	}
	config.RootCAs = roots
	return config, nil
}

func (t *connectorTransport) RoundTripHTTP(ctx context.Context, request connector.HTTPRequest) (connector.HTTPResponse, error) {
	if t == nil || t.httpClient == nil {
		return connector.HTTPResponse{}, errors.New("Connector HTTP transport is unavailable")
	}
	limit := request.MaxResponseBytes
	if limit == 0 {
		limit = defaultConnectorHTTPResponseBytes
	}
	if limit < 1 || limit > maxConnectorHTTPResponseBytes {
		return connector.HTTPResponse{}, fmt.Errorf("Connector HTTP response limit must be between 1 and %d bytes", maxConnectorHTTPResponseBytes)
	}
	body, err := injectConnectorSecretForm(request.Headers, request.Body, request.SecretForm)
	if err != nil {
		return connector.HTTPResponse{}, err
	}
	body, err = injectConnectorSecretJSON(request.Headers, body, request.SecretJSON)
	if err != nil {
		return connector.HTTPResponse{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, strings.TrimSpace(request.Method), strings.TrimSpace(request.URL), bytes.NewReader(body))
	if err != nil {
		return connector.HTTPResponse{}, fmt.Errorf("build Connector HTTP request: %w", err)
	}
	if err := injectConnectorSecretQuery(httpRequest.URL, request.SecretQuery); err != nil {
		return connector.HTTPResponse{}, err
	}
	for name, values := range request.Headers {
		for _, value := range values {
			httpRequest.Header.Add(name, value)
		}
	}
	if err := injectConnectorSecretHeaders(httpRequest.Header, request.SecretHeaders); err != nil {
		return connector.HTTPResponse{}, err
	}
	client := t.httpClient
	if _, bounded := ctx.Deadline(); bounded {
		// The operation context already bounds the entire exchange. A second,
		// fixed client timeout would truncate longer operations such as OCR.
		// Copy the client so concurrent requests keep their own timeout policy.
		scoped := *client
		scoped.Timeout = 0
		client = &scoped
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return connector.HTTPResponse{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return connector.HTTPResponse{}, fmt.Errorf("read Connector HTTP response: %w", err)
	}
	if int64(len(responseBody)) > limit {
		return connector.HTTPResponse{}, fmt.Errorf("Connector HTTP response exceeds %d bytes", limit)
	}
	return connector.HTTPResponse{StatusCode: response.StatusCode, Headers: cloneHTTPHeaders(response.Header), Body: responseBody}, nil
}

func injectConnectorSecretHeaders(headers http.Header, secrets map[string][]string) error {
	for name, values := range secrets {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if canonical == "" || canonical != name {
			return errors.New("Connector HTTP secret header name must be canonical and non-empty")
		}
		if _, exists := headers[canonical]; exists {
			return fmt.Errorf("Connector HTTP secret header %q conflicts with public headers", canonical)
		}
		if len(values) == 0 {
			return fmt.Errorf("Connector HTTP secret header %q has no values", canonical)
		}
		for _, value := range values {
			if value == "" || strings.ContainsAny(value, "\r\n") {
				return fmt.Errorf("Connector HTTP secret header %q has an invalid value", canonical)
			}
		}
	}
	for name, values := range secrets {
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	return nil
}

func injectConnectorSecretForm(headers map[string][]string, body []byte, secrets map[string]string) ([]byte, error) {
	if len(secrets) == 0 {
		return body, nil
	}
	contentType := ""
	for name, values := range headers {
		if strings.EqualFold(name, "Content-Type") && len(values) > 0 {
			contentType = values[0]
			break
		}
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return nil, errors.New("Connector HTTP secret form requires application/x-www-form-urlencoded Content-Type")
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, errors.New("Connector HTTP public form body is invalid")
	}
	for key, value := range secrets {
		if key == "" || key != strings.TrimSpace(key) {
			return nil, errors.New("Connector HTTP secret form key must be canonical and non-empty")
		}
		if value == "" {
			return nil, fmt.Errorf("Connector HTTP secret form value for %q is empty", key)
		}
		if _, exists := form[key]; exists {
			return nil, fmt.Errorf("Connector HTTP secret form key %q conflicts with the public body", key)
		}
		form.Set(key, value)
	}
	return []byte(form.Encode()), nil
}

func injectConnectorSecretJSON(headers map[string][]string, body []byte, secrets map[string]string) ([]byte, error) {
	if len(secrets) == 0 {
		return body, nil
	}
	contentType := ""
	for name, values := range headers {
		if strings.EqualFold(name, "Content-Type") && len(values) > 0 {
			contentType = values[0]
			break
		}
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return nil, errors.New("Connector HTTP secret JSON requires application/json Content-Type")
	}
	object := map[string]json.RawMessage{}
	if len(body) == 0 || json.Unmarshal(body, &object) != nil {
		return nil, errors.New("Connector HTTP public JSON body must be an object")
	}
	for key, value := range secrets {
		if key == "" || key != strings.TrimSpace(key) {
			return nil, errors.New("Connector HTTP secret JSON key must be canonical and non-empty")
		}
		if value == "" {
			return nil, fmt.Errorf("Connector HTTP secret JSON value for %q is empty", key)
		}
		if _, exists := object[key]; exists {
			return nil, fmt.Errorf("Connector HTTP secret JSON key %q conflicts with the public body", key)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode Connector HTTP secret JSON field %q", key)
		}
		object[key] = encoded
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, errors.New("encode Connector HTTP JSON body")
	}
	return encoded, nil
}

func injectConnectorSecretQuery(target *url.URL, secrets map[string]string) error {
	if len(secrets) == 0 {
		return nil
	}
	query := target.Query()
	for key, value := range secrets {
		if key == "" || key != strings.TrimSpace(key) {
			return errors.New("Connector HTTP secret query key must be canonical and non-empty")
		}
		if value == "" {
			return fmt.Errorf("Connector HTTP secret query value for %q is empty", key)
		}
		if _, exists := query[key]; exists {
			return fmt.Errorf("Connector HTTP secret query key %q conflicts with the request URL", key)
		}
		query.Set(key, value)
	}
	target.RawQuery = query.Encode()
	return nil
}

func (t *connectorTransport) ExecuteSQL(ctx context.Context, request connector.SQLRequest) (result connector.SQLResult, returnErr error) {
	driver := strings.TrimSpace(request.Driver)
	if !allowedConnectorSQLDriver(driver) {
		return result, fmt.Errorf("Connector SQL driver %q is unsupported", request.Driver)
	}
	if strings.TrimSpace(request.DSN) == "" {
		return result, errors.New("Connector SQL DSN is required")
	}
	requestCtx := ctx
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	database, err := sql.Open(driver, request.DSN)
	if err != nil {
		return result, err
	}
	defer func() {
		if closeErr := database.Close(); returnErr == nil && closeErr != nil {
			returnErr = closeErr
		}
	}()
	switch request.Operation {
	case connector.SQLOperationPing:
		return result, database.PingContext(requestCtx)
	case connector.SQLOperationQuery:
		return queryConnectorSQL(requestCtx, database, request)
	case connector.SQLOperationExec:
		return execConnectorSQL(requestCtx, database, request)
	default:
		return result, fmt.Errorf("Connector SQL operation %q is unsupported", request.Operation)
	}
}

func allowedConnectorSQLDriver(driver string) bool {
	switch driver {
	case "mysql", "pgx", "sqlserver":
		return true
	default:
		return false
	}
}

func queryConnectorSQL(ctx context.Context, database *sql.DB, request connector.SQLRequest) (connector.SQLResult, error) {
	if strings.TrimSpace(request.Statement) == "" {
		return connector.SQLResult{}, errors.New("Connector SQL query statement is required")
	}
	limit := request.MaxRows
	if limit == 0 {
		limit = defaultConnectorSQLRows
	}
	if limit < 1 || limit > maxConnectorSQLRows {
		return connector.SQLResult{}, fmt.Errorf("Connector SQL max rows must be between 1 and %d", maxConnectorSQLRows)
	}
	rows, err := database.QueryContext(ctx, request.Statement, request.Arguments...)
	if err != nil {
		return connector.SQLResult{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return connector.SQLResult{}, err
	}
	result := connector.SQLResult{Columns: append([]string(nil), columns...)}
	for rows.Next() {
		if len(result.Rows) == limit {
			result.Truncated = true
			break
		}
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return connector.SQLResult{}, err
		}
		for index, value := range values {
			if raw, ok := value.([]byte); ok {
				values[index] = append([]byte(nil), raw...)
			}
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return connector.SQLResult{}, err
	}
	return result, nil
}

func execConnectorSQL(ctx context.Context, database *sql.DB, request connector.SQLRequest) (connector.SQLResult, error) {
	if strings.TrimSpace(request.Statement) == "" {
		return connector.SQLResult{}, errors.New("Connector SQL exec statement is required")
	}
	executed, err := database.ExecContext(ctx, request.Statement, request.Arguments...)
	if err != nil {
		return connector.SQLResult{}, err
	}
	result := connector.SQLResult{}
	if affected, affectedErr := executed.RowsAffected(); affectedErr == nil {
		result.RowsAffected = affected
	}
	if inserted, insertedErr := executed.LastInsertId(); insertedErr == nil {
		result.LastInsertID = &inserted
	}
	return result, nil
}

func cloneHTTPHeaders(input http.Header) map[string][]string {
	result := make(map[string][]string, len(input))
	for name, values := range input {
		result[name] = append([]string(nil), values...)
	}
	return result
}
