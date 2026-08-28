package auditmodel

import "testing"

func TestClassifyAuditEventUsesStablePrecedence(t *testing.T) {
	tests := []struct {
		name  string
		event AuditEvent
		want  string
	}{
		{
			name:  "business by default",
			event: AuditEvent{Event: "order.updated", ObjectKey: "fulfillment_order"},
			want:  AuditEventClassBusiness,
		},
		{
			name:  "governance marker",
			event: AuditEvent{Event: "identity_role_updated", ObjectKey: "role"},
			want:  AuditEventClassGovernance,
		},
		{
			name:  "operations wins over governance",
			event: AuditEvent{Event: "identity_recovery_retry", ObjectKey: "role"},
			want:  AuditEventClassOperations,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyAuditEvent(test.event); got != test.want {
				t.Fatalf("ClassifyAuditEvent()=%q want %q", got, test.want)
			}
		})
	}
}

func TestAuditEventClassMarkersReturnsACopy(t *testing.T) {
	markers := AuditEventClassMarkers(AuditEventClassOperations)
	if len(markers) == 0 {
		t.Fatal("operations markers are empty")
	}
	markers[0] = "changed"
	if AuditEventClassMarkers(AuditEventClassOperations)[0] == "changed" {
		t.Fatal("caller mutated the shared marker catalog")
	}
	if markers := AuditEventClassMarkers("unknown"); markers != nil {
		t.Fatalf("unknown class markers=%v", markers)
	}
}

func TestAuditEventCursorRoundTripAndInvalidInputs(t *testing.T) {
	event := AuditEvent{ID: "audit-equal-2", CreatedAt: "2026-08-18T12:00:00.123456789Z"}
	cursor := EncodeAuditEventCursor(event)
	decoded, err := DecodeAuditEventCursor(cursor)
	if err != nil || decoded.ID != event.ID || decoded.CreatedAt != event.CreatedAt {
		t.Fatalf("cursor=%q decoded=%+v err=%v", cursor, decoded, err)
	}
	for _, invalid := range []string{"", "not-base64", "e30", "eyJ2IjoyLCJjcmVhdGVkX2F0Ijoibm93IiwiaWQiOiJpZCJ9"} {
		if _, err := DecodeAuditEventCursor(invalid); err == nil {
			t.Fatalf("invalid cursor %q was accepted", invalid)
		}
	}
}
