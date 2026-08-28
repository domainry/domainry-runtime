package runtimehost

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
)

const maxConnectorMQTTPayloadBytes = 1 << 20

func (t *connectorTransport) ExecuteMQTT(ctx context.Context, request connector.MQTTRequest) (result connector.MQTTResult, returnErr error) {
	if t == nil {
		return result, errors.New("Connector MQTT transport is unavailable")
	}
	endpoint, err := url.Parse(strings.TrimSpace(request.BrokerURL))
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Scheme != "mqtt" && endpoint.Scheme != "mqtts") {
		return result, errors.New("Connector MQTT broker URL is invalid")
	}
	if endpoint.Scheme == "mqtt" && !connectorMQTTLoopback(endpoint.Hostname()) {
		return result, errors.New("Connector plaintext MQTT is restricted to loopback")
	}
	if request.QoS != 0 && request.QoS != 1 {
		return result, errors.New("Connector MQTT supports QoS 0 or 1 only")
	}
	if !request.ProbeOnly && (request.Topic == "" || len(request.Topic) > 65535 || strings.ContainsAny(request.Topic, "\x00#+") || len(request.Payload) == 0 || len(request.Payload) > maxConnectorMQTTPayloadBytes) {
		return result, errors.New("Connector MQTT topic and payload are invalid")
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if timeout > 5*time.Minute {
		return result, errors.New("Connector MQTT timeout exceeds five minutes")
	}
	port := endpoint.Port()
	if port == "" {
		if endpoint.Scheme == "mqtts" {
			port = "8883"
		} else {
			port = "1883"
		}
	}
	connection, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", net.JoinHostPort(endpoint.Hostname(), port))
	if err != nil {
		return result, err
	}
	defer connection.Close()
	if endpoint.Scheme == "mqtts" {
		tlsConfig, configErr := connectorMQTTTLSConfig(endpoint.Hostname(), request)
		if configErr != nil {
			return result, configErr
		}
		tlsConnection := tls.Client(connection, tlsConfig)
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return result, err
		}
		connection = tlsConnection
	}
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return result, err
	}
	client := &connectorMQTTClient{connection: connection, reader: bufio.NewReader(connection)}
	if err := client.connect(ctx, request); err != nil {
		return result, err
	}
	result.Connected = true
	defer func() { _ = client.disconnect() }()
	if request.ProbeOnly {
		return result, nil
	}
	packetID, err := client.publish(ctx, request.Topic, request.Payload, request.QoS)
	if err != nil {
		return result, err
	}
	result.Accepted, result.PacketID = true, packetID
	return result, nil
}

func connectorMQTTTLSConfig(host string, request connector.MQTTRequest) (*tls.Config, error) {
	serverName := strings.TrimSpace(request.TLSServerName)
	if serverName == "" {
		serverName = host
	}
	config := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(request.TLSCAPEM) != "" {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(request.TLSCAPEM)) {
			return nil, errors.New("Connector MQTT TLS CA PEM is invalid")
		}
		config.RootCAs = roots
	}
	certificate, privateKey := strings.TrimSpace(request.SecretCertificate), strings.TrimSpace(request.SecretPrivateKey)
	if certificate != "" || privateKey != "" {
		if certificate == "" || privateKey == "" {
			return nil, errors.New("Connector MQTT client certificate and private key must be provided together")
		}
		pair, err := tls.X509KeyPair([]byte(certificate), []byte(privateKey))
		if err != nil {
			return nil, errors.New("Connector MQTT client certificate is invalid")
		}
		config.Certificates = []tls.Certificate{pair}
	}
	return config, nil
}

type connectorMQTTClient struct {
	connection net.Conn
	reader     *bufio.Reader
	packetID   atomic.Uint32
}

func (c *connectorMQTTClient) connect(ctx context.Context, request connector.MQTTRequest) error {
	clientID := strings.TrimSpace(request.ClientID)
	if clientID == "" {
		clientID = "domainry-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if len(clientID) > 65535 {
		return errors.New("Connector MQTT client ID is too long")
	}
	flags := byte(0x02)
	payload := connectorMQTTAppendUTF8(nil, clientID)
	if request.SecretUsername != "" {
		flags |= 0x80
		payload = connectorMQTTAppendUTF8(payload, request.SecretUsername)
	}
	if request.SecretPassword != "" {
		if request.SecretUsername == "" {
			return errors.New("Connector MQTT username is required with password")
		}
		flags |= 0x40
		payload = connectorMQTTAppendUTF8(payload, request.SecretPassword)
	}
	variable := []byte{0, 4, 'M', 'Q', 'T', 'T', 4, flags, 0, 60}
	if err := c.writePacket(ctx, 0x10, append(variable, payload...)); err != nil {
		return err
	}
	header, body, err := c.readPacket(ctx)
	if err != nil {
		return err
	}
	if header != 0x20 || len(body) != 2 || body[1] != 0 {
		return errors.New("Connector MQTT broker rejected CONNECT")
	}
	return nil
}
func (c *connectorMQTTClient) publish(ctx context.Context, topic string, payload []byte, qos byte) (uint16, error) {
	body := connectorMQTTAppendUTF8(nil, topic)
	header := byte(0x30)
	var id uint16
	if qos == 1 {
		header = 0x32
		id = uint16(c.packetID.Add(1))
		if id == 0 {
			id = uint16(c.packetID.Add(1))
		}
		body = binary.BigEndian.AppendUint16(body, id)
	}
	body = append(body, payload...)
	if err := c.writePacket(ctx, header, body); err != nil {
		return 0, err
	}
	if qos == 0 {
		return 0, nil
	}
	responseHeader, response, err := c.readPacket(ctx)
	if err != nil {
		return 0, err
	}
	if responseHeader != 0x40 || len(response) != 2 || binary.BigEndian.Uint16(response) != id {
		return 0, errors.New("Connector MQTT PUBACK is invalid")
	}
	return id, nil
}
func (c *connectorMQTTClient) writePacket(ctx context.Context, header byte, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	packet := append([]byte{header}, connectorMQTTEncodeRemaining(len(body))...)
	packet = append(packet, body...)
	_, err := c.connection.Write(packet)
	return err
}
func (c *connectorMQTTClient) readPacket(ctx context.Context) (byte, []byte, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	header, err := c.reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	length, err := connectorMQTTDecodeRemaining(c.reader)
	if err != nil || length > 4<<20 {
		return 0, nil, errors.New("Connector MQTT packet is invalid")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return 0, nil, err
	}
	return header, body, nil
}
func (c *connectorMQTTClient) disconnect() error {
	_, err := c.connection.Write([]byte{0xe0, 0})
	return err
}
func connectorMQTTAppendUTF8(target []byte, value string) []byte {
	target = binary.BigEndian.AppendUint16(target, uint16(len(value)))
	return append(target, []byte(value)...)
}
func connectorMQTTEncodeRemaining(value int) []byte {
	result := []byte{}
	for {
		digit := byte(value % 128)
		value /= 128
		if value > 0 {
			digit |= 0x80
		}
		result = append(result, digit)
		if value == 0 {
			return result
		}
	}
}
func connectorMQTTDecodeRemaining(reader *bufio.Reader) (int, error) {
	multiplier, value := 1, 0
	for index := 0; index < 4; index++ {
		digit, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		value += int(digit&127) * multiplier
		if digit&128 == 0 {
			return value, nil
		}
		multiplier *= 128
	}
	return 0, errors.New("MQTT remaining length is invalid")
}
func connectorMQTTLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

var _ connector.MQTTTransport = (*connectorTransport)(nil)
