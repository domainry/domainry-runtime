package runtimehost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	connector "github.com/domainry/domainry-connector-sdk"
)

func TestConnectorWebSocketTransportUsesGovernedFramedSession(t *testing.T) {
	receivedAuthorization := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedAuthorization <- request.Header.Get("Authorization")
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		messageType, message, err := connection.Read(request.Context())
		if err == nil {
			_ = connection.Write(request.Context(), messageType, append([]byte("echo:"), message...))
			_, _, _ = connection.Read(request.Context())
		}
	}))
	defer server.Close()

	transport := newConnectorTransport().(connector.WebSocketTransport)
	session, err := transport.OpenWebSocket(t.Context(), connector.WebSocketRequest{
		URL: strings.Replace(server.URL, "http://", "ws://", 1), SecretHeaders: map[string][]string{"Authorization": {"Bot test-secret"}}, MaxMessageBytes: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Send(t.Context(), connector.WebSocketMessage{Type: connector.WebSocketMessageText, Data: []byte("hello")}); err != nil {
		t.Fatal(err)
	}
	message, err := session.Receive(t.Context())
	if err != nil || message.Type != connector.WebSocketMessageText || string(message.Data) != "echo:hello" {
		t.Fatalf("message=%+v err=%v", message, err)
	}
	if authorization := <-receivedAuthorization; authorization != "Bot test-secret" {
		t.Fatalf("authorization=%q", authorization)
	}
	closeContext, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := session.Close(closeContext); err != nil {
		t.Fatal(err)
	}
}

func TestConnectorWebSocketTransportFailsClosed(t *testing.T) {
	transport := newConnectorTransport().(connector.WebSocketTransport)
	for _, request := range []connector.WebSocketRequest{
		{URL: "ws://remote.example/socket"},
		{URL: "https://example.test/socket"},
		{URL: "wss://user@example.test/socket"},
		{URL: "wss://example.test/socket#fragment"},
		{URL: "wss://example.test/socket", MaxMessageBytes: maxConnectorWebSocketMessageBytes + 1},
		{URL: "wss://example.test/socket", Headers: map[string][]string{"authorization": {"public"}}},
		{URL: "wss://example.test/socket", Headers: map[string][]string{"Authorization": {"public"}}, SecretHeaders: map[string][]string{"Authorization": {"secret"}}},
		{URL: "wss://example.test/socket", SecretHeaders: map[string][]string{"Sec-Websocket-Key": {"secret"}}},
		{URL: "wss://example.test/socket", Subprotocols: []string{"invalid,protocol"}},
	} {
		if _, err := transport.OpenWebSocket(t.Context(), request); err == nil {
			t.Fatalf("accepted request=%+v", request)
		}
	}
}
