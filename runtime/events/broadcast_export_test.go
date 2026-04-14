package events

import "time"

// WithOnSubscribed returns a copy of cfg with the test-only onSubscribed
// hook set. Exported only to tests in events_test via the _test.go suffix.
func WithOnSubscribed(cfg BroadcastConfig, fn func()) BroadcastConfig {
	cfg.onSubscribed = fn
	return cfg
}

// ReapExpired drives MemoryTicketStore's eviction path from tests without
// waiting for the janitor's ticker.
func ReapExpired(s *MemoryTicketStore, now time.Time) { s.reapExpired(now) }
