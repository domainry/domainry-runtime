package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	connector "github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-foundation/telemetry"
)

const (
	defaultConnectorWebSocketMessageBytes = int64(1 << 20)
	maxConnectorWebSocketMessageBytes     = int64(40 << 20)
)

func (t *connectorTransport) OpenWebSocket(ctx context.Context, request connector.WebSocketRequest) (session connector.WebSocketSession, returnErr error) {
	started := time.Now()
	endpoint, err := url.Parse(strings.TrimSpace(request.URL))
	host, scheme := "", ""
	if endpoint != nil {
		host, scheme = endpoint.Hostname(), endpoint.Scheme
	}
	defer func() {
		recordConnectorDependency(ctx, telemetry.DiagnosticFeatureHTTPClient, "connector.websocket", "open", map[string]any{
			"host": host, "scheme": scheme, "subprotocol_count": len(request.Subprotocols), "max_message_bytes": request.MaxMessageBytes,
		}, map[string]any{"connected": session != nil}, started, returnErr)
	}()
	if t == nil || t.webSocketClient == nil {
		return nil, errors.New("Connector WebSocket transport is unavailable")
	}
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" || (scheme != "ws" && scheme != "wss") {
		return nil, errors.New("Connector WebSocket URL is invalid")
	}
	if scheme == "ws" && !connectorWebSocketLoopback(host) {
		return nil, errors.New("Connector plaintext WebSocket is restricted to loopback")
	}
	limit := request.MaxMessageBytes
	if limit == 0 {
		limit = defaultConnectorWebSocketMessageBytes
	}
	if limit < 1 || limit > maxConnectorWebSocketMessageBytes {
		return nil, fmt.Errorf("Connector WebSocket message limit must be between 1 and %d bytes", maxConnectorWebSocketMessageBytes)
	}
	headers, err := connectorWebSocketHeaders(request.Headers, request.SecretHeaders)
	if err != nil {
		return nil, err
	}
	for _, protocol := range request.Subprotocols {
		if protocol == "" || protocol != strings.TrimSpace(protocol) || strings.ContainsAny(protocol, "\r\n,") {
			return nil, errors.New("Connector WebSocket subprotocol is invalid")
		}
	}
	connection, response, err := websocket.Dial(ctx, request.URL, &websocket.DialOptions{
		HTTPClient: t.webSocketClient, HTTPHeader: headers, Subprotocols: append([]string(nil), request.Subprotocols...), CompressionMode: websocket.CompressionDisabled,
	})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	connection.SetReadLimit(limit)
	return &connectorWebSocketSession{connection: connection, limit: limit}, nil
}

func connectorWebSocketHeaders(public, secrets map[string][]string) (http.Header, error) {
	headers := make(http.Header, len(public)+len(secrets))
	for name, values := range public {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if canonical == "" || canonical != name || connectorWebSocketReservedHeader(canonical) || len(values) == 0 {
			return nil, fmt.Errorf("Connector WebSocket public header %q is invalid", name)
		}
		for _, value := range values {
			if value == "" || strings.ContainsAny(value, "\r\n") {
				return nil, fmt.Errorf("Connector WebSocket public header %q has an invalid value", canonical)
			}
			headers.Add(canonical, value)
		}
	}
	for name := range secrets {
		if connectorWebSocketReservedHeader(name) {
			return nil, fmt.Errorf("Connector WebSocket secret header %q is reserved", name)
		}
	}
	if err := injectConnectorSecretHeaders(headers, secrets); err != nil {
		return nil, err
	}
	return headers, nil
}

func connectorWebSocketReservedHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Host", "Origin", "Sec-Websocket-Accept", "Sec-Websocket-Extensions", "Sec-Websocket-Key", "Sec-Websocket-Protocol", "Sec-Websocket-Version", "Upgrade":
		return true
	default:
		return false
	}
}

func connectorWebSocketLoopback(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

type connectorWebSocketSession struct {
	connection *websocket.Conn
	limit      int64
}

func (s *connectorWebSocketSession) Send(ctx context.Context, message connector.WebSocketMessage) (returnErr error) {
	started := time.Now()
	defer func() {
		recordConnectorDependency(ctx, telemetry.DiagnosticFeatureHTTPClient, "connector.websocket", "send", map[string]any{
			"message_type": string(message.Type), "message_bytes": len(message.Data),
		}, nil, started, returnErr)
	}()
	if s == nil || s.connection == nil {
		return errors.New("Connector WebSocket session is closed")
	}
	if int64(len(message.Data)) > s.limit {
		return errors.New("Connector WebSocket message exceeds the configured limit")
	}
	messageType, err := connectorWebSocketMessageType(message.Type)
	if err != nil {
		return err
	}
	return s.connection.Write(ctx, messageType, message.Data)
}

func (s *connectorWebSocketSession) Receive(ctx context.Context) (message connector.WebSocketMessage, returnErr error) {
	started := time.Now()
	defer func() {
		recordConnectorDependency(ctx, telemetry.DiagnosticFeatureHTTPClient, "connector.websocket", "receive", nil, map[string]any{
			"message_type": string(message.Type), "message_bytes": len(message.Data),
		}, started, returnErr)
	}()
	if s == nil || s.connection == nil {
		return message, errors.New("Connector WebSocket session is closed")
	}
	messageType, data, err := s.connection.Read(ctx)
	if err != nil {
		return message, err
	}
	if int64(len(data)) > s.limit {
		return message, errors.New("Connector WebSocket message exceeds the configured limit")
	}
	typeValue, err := connectorMessageType(messageType)
	if err != nil {
		return message, err
	}
	return connector.WebSocketMessage{Type: typeValue, Data: data}, nil
}

func (s *connectorWebSocketSession) Close(ctx context.Context) (returnErr error) {
	started := time.Now()
	defer func() {
		recordConnectorDependency(ctx, telemetry.DiagnosticFeatureHTTPClient, "connector.websocket", "close", nil, nil, started, returnErr)
	}()
	if s == nil || s.connection == nil {
		return nil
	}
	connection := s.connection
	s.connection = nil
	if err := ctx.Err(); err != nil {
		_ = connection.CloseNow()
		return err
	}
	return connection.Close(websocket.StatusNormalClosure, "")
}

func connectorWebSocketMessageType(messageType connector.WebSocketMessageType) (websocket.MessageType, error) {
	switch messageType {
	case connector.WebSocketMessageText:
		return websocket.MessageText, nil
	case connector.WebSocketMessageBinary:
		return websocket.MessageBinary, nil
	default:
		return 0, errors.New("Connector WebSocket message type is invalid")
	}
}

func connectorMessageType(messageType websocket.MessageType) (connector.WebSocketMessageType, error) {
	switch messageType {
	case websocket.MessageText:
		return connector.WebSocketMessageText, nil
	case websocket.MessageBinary:
		return connector.WebSocketMessageBinary, nil
	default:
		return "", errors.New("Connector WebSocket message type is invalid")
	}
}

var _ connector.WebSocketTransport = (*connectorTransport)(nil)
var _ connector.WebSocketSession = (*connectorWebSocketSession)(nil)
