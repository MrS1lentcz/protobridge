package events

// WithOnSubscribed returns a copy of cfg with the test-only onSubscribed
// hook set. Exported only to tests in events_test via the _test.go suffix.
func WithOnSubscribed(cfg BroadcastConfig, fn func()) BroadcastConfig {
	cfg.onSubscribed = fn
	return cfg
}
