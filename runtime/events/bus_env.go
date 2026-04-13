package events

import (
	"fmt"
	"os"
	"strings"
)

// NewBusFromEnv returns a Bus configured from PROTOBRIDGE_BUS_URL. It is the
// entry point generated main.go uses to stand up broadcast services without
// knowing which backend is deployed.
//
// Supported schemes:
//
//	(empty), memory://       — in-process gochannel bus (dev + tests)
//
// Real-backend schemes (nats://, redis://, amqp://) are reserved — they will
// return a clear error pointing users to construct a *WatermillBus directly
// until the matching Watermill driver is wired into the runtime. This keeps
// the runtime's dependency footprint small while leaving the convention in
// place for codegen.
func NewBusFromEnv() (Bus, error) {
	raw := os.Getenv("PROTOBRIDGE_BUS_URL")
	scheme, _, _ := strings.Cut(raw, "://")
	switch strings.ToLower(scheme) {
	case "", "memory":
		return NewInMemoryBus(), nil
	case "nats", "redis", "amqp":
		return nil, fmt.Errorf("events: PROTOBRIDGE_BUS_URL scheme %q is reserved but not yet wired — construct a *events.WatermillBus directly and skip NewBusFromEnv", scheme)
	default:
		return nil, fmt.Errorf("events: unsupported PROTOBRIDGE_BUS_URL %q", raw)
	}
}
