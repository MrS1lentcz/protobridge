package events_test

import (
	"context"
	"testing"

	"github.com/mrs1lentcz/protobridge/runtime/events"
)

func TestWithLabels_StoresAndComposes(t *testing.T) {
	ctx := events.WithLabels(context.Background(), "tenant_id", "abc")
	ctx = events.WithLabels(ctx, "project_id", "xyz", "tenant_id", "override")

	got := events.LabelsFromContext(ctx)
	if got["tenant_id"] != "override" {
		t.Errorf("later WithLabels should override; got %v", got)
	}
	if got["project_id"] != "xyz" {
		t.Errorf("earlier label should survive; got %v", got)
	}
}

func TestLabelsFromContext_EmptyByDefault(t *testing.T) {
	got := events.LabelsFromContext(context.Background())
	if got == nil {
		t.Fatal("LabelsFromContext must never return nil")
	}
	if len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func TestWithLabels_OddArgsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on odd-length kvs")
		}
	}()
	// Build the bad slice indirectly so staticcheck doesn't flag the
	// intentional misuse — we WANT the panic, that's the whole test.
	odd := append([]string{"tenant_id"}, []string{}...)
	events.WithLabels(context.Background(), odd...) //nolint:staticcheck // intentional
}

func TestLabelsToHeaders_PrefixAndExplicitWins(t *testing.T) {
	headers := events.LabelsToHeaders(
		map[string]string{"tenant_id": "abc", "role": "admin"},
		map[string]string{"traceparent": "00-x", "x-protobridge-label-tenant_id": "explicit-wins"},
	)
	if headers["x-protobridge-label-tenant_id"] != "explicit-wins" {
		t.Errorf("explicit headers should override label-derived ones; got %v", headers)
	}
	if headers["x-protobridge-label-role"] != "admin" {
		t.Errorf("role label should be prefixed: %v", headers)
	}
	if headers["traceparent"] != "00-x" {
		t.Errorf("non-label headers should pass through: %v", headers)
	}
}

func TestLabelsToHeaders_EmptyInputReturnsNil(t *testing.T) {
	if got := events.LabelsToHeaders(nil, nil); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestHeadersToLabels_StripsPrefixAndIgnoresOthers(t *testing.T) {
	got := events.HeadersToLabels(map[string]string{
		"x-protobridge-label-tenant_id": "abc",
		"x-protobridge-label-role":      "admin",
		"traceparent":                   "00-x",
		"content-type":                  "application/x-protobuf",
	})
	if len(got) != 2 || got["tenant_id"] != "abc" || got["role"] != "admin" {
		t.Errorf("got %v", got)
	}
}

func TestHeadersToLabels_NoLabelsReturnsNil(t *testing.T) {
	got := events.HeadersToLabels(map[string]string{"traceparent": "x"})
	if got != nil {
		t.Errorf("expected nil when no label headers, got %v", got)
	}
}

func TestDefaultLabelMatcher(t *testing.T) {
	cases := []struct {
		name              string
		principal, event  map[string]string
		want              bool
	}{
		{"empty event matches anything", map[string]string{"tenant_id": "abc"}, nil, true},
		{"exact match", map[string]string{"tenant_id": "abc"}, map[string]string{"tenant_id": "abc"}, true},
		{"value mismatch", map[string]string{"tenant_id": "abc"}, map[string]string{"tenant_id": "xyz"}, false},
		{"event key missing on principal", nil, map[string]string{"tenant_id": "abc"}, false},
		{"principal has extra labels", map[string]string{"tenant_id": "abc", "role": "admin"}, map[string]string{"tenant_id": "abc"}, true},
		{"multi-key all must match", map[string]string{"tenant_id": "abc", "project_id": "p1"}, map[string]string{"tenant_id": "abc", "project_id": "p1"}, true},
		{"multi-key one mismatch", map[string]string{"tenant_id": "abc", "project_id": "p1"}, map[string]string{"tenant_id": "abc", "project_id": "p2"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := events.DefaultLabelMatcher(tc.principal, tc.event); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
