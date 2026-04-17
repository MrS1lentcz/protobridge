package events

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ErrTicketInvalid is returned by TicketStore.Redeem when the ticket is
// unknown, expired, or already consumed. Callers should treat all three
// indistinguishably — surfacing the reason would let an attacker probe
// ticket validity.
var ErrTicketInvalid = errors.New("events: ticket invalid or expired")

// TicketStore issues and redeems short-lived, one-shot tickets bound to a
// set of principal labels. It enables browser clients (which can't send
// custom headers on EventSource) to authenticate SSE connections without
// putting bearer tokens in URLs.
//
// Flow: FE POSTs to the issuer endpoint with its normal auth header →
// issuer resolves principal labels and calls Issue → returns ticket →
// FE opens EventSource with ?ticket=<t> → hub calls Redeem → binds
// connection to the labels.
//
// Tickets are one-shot: a successful Redeem MUST make the ticket invalid
// for any future Redeem call (otherwise the ticket becomes a plain token
// with its own lifetime). TTL is the upper bound; Redeem removes it
// regardless of remaining TTL.
//
// Multi-replica deployments need a shared backend (Redis, DB) because the
// issuer and the hub may run on different pods. The in-memory default is
// suitable for single-replica and for tests.
type TicketStore interface {
	Issue(ctx context.Context, labels map[string]string, ttl time.Duration) (string, error)
	Redeem(ctx context.Context, ticket string) (map[string]string, error)
}

// NewMemoryTicketStore returns an in-process TicketStore. Safe for
// concurrent use. A background janitor evicts expired entries every minute
// so memory does not grow with abandoned tickets. Call Close when done to
// stop the janitor (optional — the store is usable without Close, the
// janitor just keeps running).
func NewMemoryTicketStore() *MemoryTicketStore {
	s := &MemoryTicketStore{
		entries: make(map[string]memoryTicket),
		stop:    make(chan struct{}),
	}
	go s.janitor()
	return s
}

// MemoryTicketStore is the default in-process TicketStore. Exported so
// tests and single-replica deployments can construct it by name.
type MemoryTicketStore struct {
	mu       sync.Mutex
	entries  map[string]memoryTicket
	stop     chan struct{}
	stopOnce sync.Once
}

type memoryTicket struct {
	labels    map[string]string
	expiresAt time.Time
}

// Issue generates a cryptographically random ticket, stores it with the
// given labels, and returns the ticket string. ttl <= 0 is treated as
// 30 seconds — a sane default for the browser round-trip from fetch to
// EventSource connect.
func (s *MemoryTicketStore) Issue(_ context.Context, labels map[string]string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	ticket := base64.RawURLEncoding.EncodeToString(buf[:])

	// Copy labels so later mutations by the caller don't affect the
	// stored ticket. Nil maps stay nil — no point allocating an empty
	// one just to hand it back on Redeem.
	var copied map[string]string
	if labels != nil {
		copied = make(map[string]string, len(labels))
		for k, v := range labels {
			copied[k] = v
		}
	}

	s.mu.Lock()
	s.entries[ticket] = memoryTicket{labels: copied, expiresAt: time.Now().Add(ttl)}
	s.mu.Unlock()
	return ticket, nil
}

// Redeem looks up and removes the ticket. Returns ErrTicketInvalid if
// unknown, expired, or already consumed — callers should map this to 401.
func (s *MemoryTicketStore) Redeem(_ context.Context, ticket string) (map[string]string, error) {
	if ticket == "" {
		return nil, ErrTicketInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[ticket]
	if !ok {
		return nil, ErrTicketInvalid
	}
	delete(s.entries, ticket)
	if time.Now().After(entry.expiresAt) {
		return nil, ErrTicketInvalid
	}
	return entry.labels, nil
}

// Close stops the background janitor. Idempotent. The store remains
// usable after Close but expired tickets accumulate until the process
// exits — only meaningful in long-running test suites.
func (s *MemoryTicketStore) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
}

func (s *MemoryTicketStore) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-t.C:
			s.reapExpired(now)
		}
	}
}

// reapExpired evicts entries whose deadline has passed. Factored out so
// tests can drive the eviction path without waiting a minute for the
// janitor's ticker to fire.
func (s *MemoryTicketStore) reapExpired(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.entries {
		if now.After(e.expiresAt) {
			delete(s.entries, k)
		}
	}
}

// TicketIssuerConfig wires the HTTP endpoint clients POST to in order to
// exchange their normal auth credentials (Authorization header, cookie,
// …) for a short-lived ticket they can then pass as ?ticket= on the SSE
// request.
type TicketIssuerConfig struct {
	// Principal resolves labels for the incoming request. Typically the
	// same function used as BroadcastConfig.PrincipalLabels — sharing it
	// guarantees that tickets carry the exact label set the hub would
	// otherwise compute on a direct WS upgrade. Required.
	Principal func(r *http.Request) (map[string]string, error)

	// Store issues/redeems tickets. Required.
	Store TicketStore

	// TTL bounds how long an issued ticket is valid before it must be
	// redeemed. Defaults to 30s — enough for the browser fetch→
	// EventSource round-trip, tight enough to limit replay windows.
	TTL time.Duration

	// Logger receives principal-resolution and issuance failures.
	// Defaults to slog.Default() when nil.
	Logger *slog.Logger
}

// Router is the minimal subset of chi.Router needed by MountIssuer —
// accepting an interface (rather than importing chi) keeps this package
// free of a hard router dependency while staying chi-compatible.
type Router interface {
	Method(method, pattern string, h http.Handler)
}

// MountIssuer creates an in-memory ticket store, mounts a POST issuer at
// path on r, and returns the store so callers can hand it to
// BroadcastConfig.TicketStore / runtime.NewWSAuth and Close it on
// shutdown. Shared by the generated broadcast and WS-auth wiring so both
// paths go through a single issuer contract.
func MountIssuer(r Router, path string, principal func(*http.Request) (map[string]string, error)) *MemoryTicketStore {
	store := NewMemoryTicketStore()
	r.Method(http.MethodPost, path, NewTicketIssuer(TicketIssuerConfig{
		Principal: principal,
		Store:     store,
	}))
	return store
}

// NewTicketIssuer returns an http.Handler that accepts POST requests,
// resolves the caller's principal via the configured function, issues a
// ticket, and returns {"ticket":"...","expires_in":<seconds>} as JSON.
//
// Mount it at a path reachable with the app's normal auth credentials
// (Bearer header, session cookie). The returned ticket is then passed as
// ?ticket= on the SSE request — see BroadcastConfig.TicketStore.
func NewTicketIssuer(cfg TicketIssuerConfig) http.Handler {
	if cfg.Principal == nil {
		panic("events: TicketIssuerConfig.Principal is required")
	}
	if cfg.Store == nil {
		panic("events: TicketIssuerConfig.Store is required")
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 30 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		labels, err := cfg.Principal(r)
		if err != nil {
			logger.Warn("events: ticket principal resolution failed",
				"remote", r.RemoteAddr, "err", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ticket, err := cfg.Store.Issue(r.Context(), labels, cfg.TTL)
		if err != nil {
			logger.Error("events: ticket issue failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		// Vary on the credentials we actually key responses on — defence
		// in depth if an upstream ever overrides Cache-Control.
		w.Header().Set("Vary", "Authorization, Cookie")
		_ = json.NewEncoder(w).Encode(struct {
			Ticket    string `json:"ticket"`
			ExpiresIn int    `json:"expires_in"`
		}{Ticket: ticket, ExpiresIn: int(cfg.TTL.Seconds())})
	})
}
