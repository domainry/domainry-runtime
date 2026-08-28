package integration

import (
	"errors"
	"reflect"
	"testing"

	intmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationScannersAndHelpers(t *testing.T) {
	wantErr := errors.New("scan failure")
	checks := []struct {
		name string
		call func(integrationConnectionScanner) error
	}{
		{"secret", func(s integrationConnectionScanner) error { _, err := scanIntegrationSecret(s); return err }},
		{"connection", func(s integrationConnectionScanner) error { _, err := scanIntegrationConnection(s); return err }},
		{"external identity", func(s integrationConnectionScanner) error { _, err := scanIntegrationExternalIdentity(s); return err }},
		{"event", func(s integrationConnectionScanner) error { _, err := scanIntegrationEvent(s); return err }},
		{"invocation", func(s integrationConnectionScanner) error { _, err := scanIntegrationInvocation(s); return err }},
		{"outbox", func(s integrationConnectionScanner) error { _, err := scanIntegrationOutboxMessage(s); return err }},
		{"webhook", func(s integrationConnectionScanner) error {
			_, err := scanIntegrationWebhookSubscription(s)
			return err
		}},
		{"api key", func(s integrationConnectionScanner) error { _, err := scanIntegrationAPIKey(s); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(reflectIntegrationScanner{}); err != nil {
				t.Fatal(err)
			}
			if err := check.call(reflectIntegrationScanner{err: wantErr}); err == nil {
				t.Fatal("scan error ignored")
			}
		})
	}
	connection, err := scanIntegrationConnection(reflectIntegrationScanner{overrides: map[int]any{6: `{"a":1}`, 7: `{"key":"ref"}`}})
	if err != nil || connection.Config["a"] == nil || connection.SecretRefs["key"] != "ref" {
		t.Fatalf("connection=%#v err=%v", connection, err)
	}
}

func TestIntegrationWebhookMatchAndIdentifierHelpers(t *testing.T) {
	for _, test := range []struct {
		types []string
		event string
		want  bool
	}{
		{nil, "", true}, {[]string{"*"}, "anything", true}, {[]string{"order.created"}, "order.created", true}, {[]string{"order.created"}, "invoice.created", false}, {[]string{"order.*"}, "order.created", true}, {[]string{"order.*"}, "invoice.created", false}, {[]string{" order.created "}, " order.created ", true},
	} {
		if got := integrationWebhookSubscriptionMatchesEvent(intmodel.IntegrationWebhookSubscription{EventTypes: test.types}, test.event); got != test.want {
			t.Fatalf("types=%v event=%q got=%v", test.types, test.event, got)
		}
	}
	if WorkspaceID(" workspace ") != "workspace" || OutboxID(" w ", " c ", " op ") == "" || OutboxDedupID("w", "c", "k", "op", "d") == "" {
		t.Fatal("identifier helper")
	}
	store := openRuntimeStore(t)
	defer store.Close()
	if integrationInvocationColumnsSQL(store) == "" || integrationOutboxColumnsSQL(store) == "" || integrationWebhookSubscriptionColumnsSQL(store) == "" || integrationAPIKeyColumnsSQL(store) == "" {
		t.Fatal("column helper")
	}
	if len(nonNilMap(nil)) != 0 || nonNilMap(map[string]any{"x": 1})["x"] == nil || len(nonNilStringMap(nil)) != 0 || nonNilStringMap(map[string]string{"x": "y"})["x"] != "y" || len(nonNilStringSlice(nil)) != 0 || nonNilStringSlice([]string{"x"})[0] != "x" {
		t.Fatal("non-nil helper")
	}
	if _, err := requireIntegrationWorkspaceID(""); err == nil {
		t.Fatal("empty workspace accepted")
	}
	if _, err := requireIntegrationMutationWorkspaceID("default", "other"); err == nil {
		t.Fatal("workspace mismatch accepted")
	}
	if value, err := requireIntegrationMutationWorkspaceID("default", " "); err != nil || value != "default" {
		t.Fatalf("workspace=%q err=%v", value, err)
	}
}

type reflectIntegrationScanner struct {
	overrides map[int]any
	err       error
}

func (s reflectIntegrationScanner) Scan(dest ...any) error {
	if s.err != nil {
		return s.err
	}
	for index, target := range dest {
		value := reflect.ValueOf(target).Elem()
		if override, ok := s.overrides[index]; ok {
			value.Set(reflect.ValueOf(override))
			continue
		}
		switch value.Kind() {
		case reflect.String:
			text := ""
			if index == 6 || index == 7 || index == 10 || index == 15 {
				text = "{}"
			}
			value.SetString(text)
		case reflect.Bool:
			value.SetBool(false)
		case reflect.Int, reflect.Int64:
			value.SetInt(0)
		case reflect.Struct:
			value.Set(reflect.Zero(value.Type()))
		default:
			return errors.New("unsupported scan destination")
		}
	}
	return nil
}
