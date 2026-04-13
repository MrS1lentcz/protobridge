package events

import (
	"strings"
	"testing"
)

func TestNewBusFromEnvDefaults(t *testing.T) {
	t.Setenv("PROTOBRIDGE_BUS_URL", "")
	bus, err := NewBusFromEnv()
	if err != nil {
		t.Fatalf("empty URL: %v", err)
	}
	if bus == nil {
		t.Fatal("expected non-nil bus")
	}
	_ = bus.(*WatermillBus).Close()
}

func TestNewBusFromEnvMemoryScheme(t *testing.T) {
	t.Setenv("PROTOBRIDGE_BUS_URL", "memory://")
	bus, err := NewBusFromEnv()
	if err != nil {
		t.Fatalf("memory://: %v", err)
	}
	_ = bus.(*WatermillBus).Close()
}

func TestNewBusFromEnvReservedSchemes(t *testing.T) {
	for _, scheme := range []string{"nats", "redis", "amqp"} {
		t.Run(scheme, func(t *testing.T) {
			t.Setenv("PROTOBRIDGE_BUS_URL", scheme+"://localhost")
			_, err := NewBusFromEnv()
			if err == nil {
				t.Fatal("expected error for reserved scheme")
			}
			if !strings.Contains(err.Error(), scheme) {
				t.Fatalf("error should mention scheme, got %q", err)
			}
		})
	}
}

func TestNewBusFromEnvUnsupported(t *testing.T) {
	t.Setenv("PROTOBRIDGE_BUS_URL", "kafka://localhost")
	_, err := NewBusFromEnv()
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}
