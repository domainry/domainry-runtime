package runtimehost

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
)

func TestConnectorMQTTTransportConnectsAndPublishesQoS1(t *testing.T) {
	address, received, closeFixture := startConnectorMQTTFixture(t)
	defer closeFixture()
	transport := newConnectorTransport().(connector.MQTTTransport)
	result, err := transport.ExecuteMQTT(t.Context(), connector.MQTTRequest{BrokerURL: "mqtt://" + address, ClientID: "client-1", Topic: "devices/1/telemetry", Payload: []byte(`{"temperature":21}`), QoS: 1, Timeout: 2 * time.Second, SecretUsername: "runtime-user", SecretPassword: "runtime-password"})
	if err != nil || !result.Connected || !result.Accepted || result.PacketID != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	message := <-received
	if message["topic"] != "devices/1/telemetry" || message["username"] != "runtime-user" || message["password"] != "runtime-password" {
		t.Fatalf("message=%v", message)
	}
}

func TestConnectorMQTTTransportFailsClosed(t *testing.T) {
	transport := newConnectorTransport().(connector.MQTTTransport)
	for _, request := range []connector.MQTTRequest{{BrokerURL: "mqtt://remote.example"}, {BrokerURL: "http://localhost"}, {BrokerURL: "mqtt://localhost", QoS: 2}, {BrokerURL: "mqtt://localhost", Topic: "devices/#", Payload: []byte(`{}`)}, {BrokerURL: "mqtt://localhost", Topic: "topic", Payload: []byte(`{}`), SecretPassword: "password"}} {
		if _, err := transport.ExecuteMQTT(t.Context(), request); err == nil {
			t.Fatalf("accepted request=%+v", request)
		}
	}
	if _, err := connectorMQTTTLSConfig("broker.example", connector.MQTTRequest{TLSCAPEM: "invalid"}); err == nil {
		t.Fatal("invalid CA accepted")
	}
	if _, err := connectorMQTTTLSConfig("broker.example", connector.MQTTRequest{SecretCertificate: "certificate"}); err == nil {
		t.Fatal("incomplete certificate pair accepted")
	}
}

func startConnectorMQTTFixture(t *testing.T) (string, <-chan map[string]string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan map[string]string, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		_, connectBody, readErr := readMQTTFixturePacket(reader)
		if readErr != nil {
			return
		}
		username, password := parseMQTTFixtureCredentials(connectBody)
		_, _ = connection.Write([]byte{0x20, 0x02, 0x00, 0x00})
		header, publishBody, readErr := readMQTTFixturePacket(reader)
		if readErr != nil || header != 0x32 {
			return
		}
		topicLength := int(binary.BigEndian.Uint16(publishBody[:2]))
		topic := string(publishBody[2 : 2+topicLength])
		offset := 2 + topicLength
		packetID := binary.BigEndian.Uint16(publishBody[offset : offset+2])
		received <- map[string]string{"topic": topic, "username": username, "password": password}
		_, _ = connection.Write([]byte{0x40, 0x02, byte(packetID >> 8), byte(packetID)})
		_, _, _ = readMQTTFixturePacket(reader)
	}()
	return listener.Addr().String(), received, func() { _ = listener.Close() }
}

func readMQTTFixturePacket(reader *bufio.Reader) (byte, []byte, error) {
	header, err := reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	length, err := connectorMQTTDecodeRemaining(reader)
	if err != nil {
		return 0, nil, err
	}
	body := make([]byte, length)
	_, err = io.ReadFull(reader, body)
	return header, body, err
}
func parseMQTTFixtureCredentials(body []byte) (string, string) {
	if len(body) < 10 {
		return "", ""
	}
	flags := body[7]
	offset := 10
	_, offset = readMQTTFixtureUTF8(body, offset)
	username, password := "", ""
	if flags&0x80 != 0 {
		username, offset = readMQTTFixtureUTF8(body, offset)
	}
	if flags&0x40 != 0 {
		password, _ = readMQTTFixtureUTF8(body, offset)
	}
	return username, password
}
func readMQTTFixtureUTF8(body []byte, offset int) (string, int) {
	length := int(binary.BigEndian.Uint16(body[offset : offset+2]))
	start := offset + 2
	return string(body[start : start+length]), start + length
}
