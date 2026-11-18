package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	connector "github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-foundation/telemetry"
)

const (
	defaultConnectorWebSocketMessageBytes = int64(1 << 20)
	maxConnectorWebSocketMessageBytes     = int64(40 << 20)
	defaultConnectorWebSocketCloseTimeout = 5 * time.Second
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
	owned := &connectorWebSocketSession{connection: connection, limit: limit}
	owned.bindContext(ctx)
	return owned, nil
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
	mu         sync.Mutex
	connection *websocket.Conn
	limit      int64
	stopOwner  func() bool
}

// bindContext makes the opening context the hard lifetime boundary for the
// upgraded connection. Providers still close sessions explicitly so peers can
// observe a normal close frame, but a missed error path cannot leave a socket,
// reader goroutine or resolved credential snapshot alive after its owner has
// been cancelled.
func (s *connectorWebSocketSession) bindContext(ctx context.Context) {
	if s == nil {
		return
	}
	stop := context.AfterFunc(ctx, s.closeNow)
	s.mu.Lock()
	if s.connection == nil {
		s.mu.Unlock()
		stop()
		return
	}
	s.stopOwner = stop
	s.mu.Unlock()
}

func (s *connectorWebSocketSession) currentConnection() *websocket.Conn {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connection
}

func (s *connectorWebSocketSession) takeConnection() (*websocket.Conn, func() bool) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	connection, stop := s.connection, s.stopOwner
	s.connection, s.stopOwner = nil, nil
	s.mu.Unlock()
	return connection, stop
}

func (s *connectorWebSocketSession) closeNow() {
	connection, stop := s.takeConnection()
	if stop != nil {
		stop()
	}
	if connection != nil {
		_ = connection.CloseNow()
	}
}

func (s *connectorWebSocketSession) Send(ctx context.Context, message connector.WebSocketMessage) (returnErr error) {
	started := time.Now()
	defer func() {
		recordConnectorDependency(ctx, telemetry.DiagnosticFeatureHTTPClient, "connector.websocket", "send", map[string]any{
			"message_type": string(message.Type), "message_bytes": len(message.Data),
		}, nil, started, returnErr)
	}()
	connection := s.currentConnection()
	if connection == nil {
		return errors.New("Connector WebSocket session is closed")
	}
	if int64(len(message.Data)) > s.limit {
		return errors.New("Connector WebSocket message exceeds the configured limit")
	}
	messageType, err := connectorWebSocketMessageType(message.Type)
	if err != nil {
		return err
	}
	return connection.Write(ctx, messageType, message.Data)
}

func (s *connectorWebSocketSession) Receive(ctx context.Context) (message connector.WebSocketMessage, returnErr error) {
	started := time.Now()
	defer func() {
		recordConnectorDependency(ctx, telemetry.DiagnosticFeatureHTTPClient, "connector.websocket", "receive", nil, map[string]any{
			"message_type": string(message.Type), "message_bytes": len(message.Data),
		}, started, returnErr)
	}()
	connection := s.currentConnection()
	if connection == nil {
		return message, errors.New("Connector WebSocket session is closed")
	}
	messageType, data, err := connection.Read(ctx)
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

func (s *connectorWebSocketSession) Close(ctx context.Context, request connector.WebSocketClose) (returnErr error) {
	started := time.Now()
	defer func() {
		recordConnectorDependency(ctx, telemetry.DiagnosticFeatureHTTPClient, "connector.websocket", "close", map[string]any{"close_code": request.Code}, nil, started, returnErr)
	}()
	if err := request.Validate(); err != nil {
		return err
	}
	connection, stop := s.takeConnection()
	if stop != nil {
		stop()
	}
	if connection == nil {
		return nil
	}
	if ctx == nil {
		_ = connection.CloseNow()
		return errors.New("Connector WebSocket close context is required")
	}
	closeContext, cancelClose := context.WithTimeout(ctx, defaultConnectorWebSocketCloseTimeout)
	defer cancelClose()
	if err := closeContext.Err(); err != nil {
		_ = connection.CloseNow()
		return err
	}
	done := make(chan error, 1)
	go func() { done <- connection.Close(websocket.StatusCode(request.Code), request.Reason) }()
	select {
	case err := <-done:
		return err
	case <-closeContext.Done():
		_ = connection.CloseNow()
		return closeContext.Err()
	}
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
