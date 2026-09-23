package runtimeengine

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestDecodeAndEncodeProjectFields(t *testing.T) {
	type opportunityFields struct {
		CustomerID  string `json:"customer"`
		AmountCents int64  `json:"amount_cents"`
		WonAt       string `json:"won_at,omitempty"`
	}
	record := Record{ID: "opportunity-one", ObjectKey: "opportunity", Fields: map[string]any{
		"customer": "customer-one", "amount_cents": float64(12500), "ignored": true,
	}}
	decoded, err := DecodeFields[opportunityFields](record)
	if err != nil || decoded.CustomerID != "customer-one" || decoded.AmountCents != 12500 || decoded.WonAt != "" {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	encoded, err := EncodeFields(opportunityFields{CustomerID: "customer-two", AmountCents: 900})
	if err != nil || encoded["customer"] != "customer-two" || fmt.Sprint(encoded["amount_cents"]) != "900" {
		t.Fatalf("encoded=%#v err=%v", encoded, err)
	}
}

func TestDecodeFieldsReturnsStableRuntimeError(t *testing.T) {
	type invalid struct {
		Amount int64 `json:"amount"`
	}
	_, err := DecodeFields[invalid](Record{ID: "one", ObjectKey: "invoice", Fields: map[string]any{"amount": "not-a-number"}})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "backend.project.record_decode_failed" || !reflect.DeepEqual(typed.Parameters, map[string]string{"object": "invoice", "record": "one"}) {
		t.Fatalf("error=%#v", err)
	}
}

func TestEncodeFieldsReturnsStableRuntimeError(t *testing.T) {
	for _, value := range []any{nil, struct {
		Callback func() `json:"callback"`
	}{Callback: func() {}}} {
		_, err := EncodeFields(value)
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != "backend.project.record_encode_failed" {
			t.Fatalf("value=%#v error=%#v", value, err)
		}
	}
}
