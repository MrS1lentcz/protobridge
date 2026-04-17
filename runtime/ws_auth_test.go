package runtime_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mrs1lentcz/protobridge/runtime"
	"github.com/mrs1lentcz/protobridge/runtime/events"
)

func mustRequest(t *testing.T, method, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	return req
}

func TestNewWSAuth_HeaderPassthrough(t *testing.T) {
	called := false
	inner := runtime.AuthFunc(func(ctx context.Context, r *http.Request) ([]byte, error) {
		called = true
		if got := r.Header.Get("Authorization"); got != "Bearer real" {
			t.Fatalf("inner saw %q, want Bearer real", got)
		}
		return []byte("ok"), nil
	})
	wrapped := runtime.NewWSAuth(runtime.WSAuthConfig{Inner: inner})

	req := mustRequest(t, http.MethodGet, "/ws")
	req.Header.Set("Authorization", "Bearer real")
	out, err := wrapped(context.Background(), req)
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	if string(out) != "ok" {
		t.Fatalf("out=%q", out)
	}
	if !called {
		t.Fatal("inner AuthFunc was not invoked")
	}
}

func TestNewWSAuth_TicketReplacesHeader(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	ctx := context.Background()
	ticket, err := store.Issue(ctx, map[string]string{
		runtime.WSAuthTicketHeaderLabel: "Bearer replayed",
	}, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	inner := runtime.AuthFunc(func(ctx context.Context, r *http.Request) ([]byte, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer replayed" {
			t.Fatalf("inner saw %q, want Bearer replayed", got)
		}
		return []byte("ticket-ok"), nil
	})
	wrapped := runtime.NewWSAuth(runtime.WSAuthConfig{
		Inner:       inner,
		TicketStore: store,
		Modes:       []string{runtime.WSAuthModeTicket},
	})

	req := mustRequest(t, http.MethodGet, "/ws?ticket="+ticket)
	out, err := wrapped(ctx, req)
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	if string(out) != "ticket-ok" {
		t.Fatalf("out=%q", out)
	}

	// Ticket is one-shot — second redeem must fail.
	req2 := mustRequest(t, http.MethodGet, "/ws?ticket="+ticket)
	if _, err := wrapped(ctx, req2); !errors.Is(err, events.ErrTicketInvalid) {
		t.Fatalf("second redeem err=%v, want ErrTicketInvalid", err)
	}
}

func TestNewWSAuth_HeaderPreferredOverTicket(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	ctx := context.Background()
	ticket, _ := store.Issue(ctx, map[string]string{
		runtime.WSAuthTicketHeaderLabel: "Bearer ticket-value",
	}, time.Minute)

	inner := runtime.AuthFunc(func(ctx context.Context, r *http.Request) ([]byte, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer header-value" {
			t.Fatalf("inner saw %q, want Bearer header-value", got)
		}
		return nil, nil
	})
	wrapped := runtime.NewWSAuth(runtime.WSAuthConfig{
		Inner:       inner,
		TicketStore: store,
		Modes:       []string{runtime.WSAuthModeHeader, runtime.WSAuthModeTicket},
	})

	req := mustRequest(t, http.MethodGet, "/ws?ticket="+ticket)
	req.Header.Set("Authorization", "Bearer header-value")
	if _, err := wrapped(ctx, req); err != nil {
		t.Fatalf("wrapped: %v", err)
	}

	// Ticket must still be redeemable — the handler skipped it entirely.
	if _, err := store.Redeem(ctx, ticket); err != nil {
		t.Fatalf("ticket was consumed even though Authorization won: %v", err)
	}
}

func TestNewWSAuth_TicketMissingAuthorizationLabel(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	ctx := context.Background()
	ticket, _ := store.Issue(ctx, map[string]string{"other": "value"}, time.Minute)

	inner := runtime.AuthFunc(func(ctx context.Context, r *http.Request) ([]byte, error) {
		t.Fatal("inner must not be called when ticket lacks authorization label")
		return nil, nil
	})
	wrapped := runtime.NewWSAuth(runtime.WSAuthConfig{
		Inner:       inner,
		TicketStore: store,
		Modes:       []string{runtime.WSAuthModeTicket},
	})
	req := mustRequest(t, http.MethodGet, "/ws?ticket="+ticket)
	if _, err := wrapped(ctx, req); err == nil {
		t.Fatal("expected error when ticket labels lack authorization key")
	}
}

func TestNewWSAuth_HeaderOnlyIgnoresTicket(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)
	ticket, _ := store.Issue(context.Background(), map[string]string{
		runtime.WSAuthTicketHeaderLabel: "Bearer t",
	}, time.Minute)

	inner := runtime.AuthFunc(func(ctx context.Context, r *http.Request) ([]byte, error) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("inner saw unexpected header %q", got)
		}
		return nil, errors.New("no credentials")
	})
	wrapped := runtime.NewWSAuth(runtime.WSAuthConfig{Inner: inner})
	req := mustRequest(t, http.MethodGet, "/ws?ticket="+ticket)

	if _, err := wrapped(context.Background(), req); err == nil {
		t.Fatal("header-only mode must not redeem tickets")
	}

	// Ticket preserved because redemption never ran.
	if _, err := store.Redeem(context.Background(), ticket); err != nil {
		t.Fatalf("ticket incorrectly consumed: %v", err)
	}
}

func TestNewWSAuth_CustomParam(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	ctx := context.Background()
	ticket, _ := store.Issue(ctx, map[string]string{
		runtime.WSAuthTicketHeaderLabel: "Bearer custom",
	}, time.Minute)

	inner := runtime.AuthFunc(func(ctx context.Context, r *http.Request) ([]byte, error) {
		return []byte(r.Header.Get("Authorization")), nil
	})
	wrapped := runtime.NewWSAuth(runtime.WSAuthConfig{
		Inner:       inner,
		TicketStore: store,
		Modes:       []string{runtime.WSAuthModeTicket},
		TicketParam: "tkt",
	})
	req := mustRequest(t, http.MethodGet, "/ws?tkt="+ticket)
	out, err := wrapped(ctx, req)
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	if string(out) != "Bearer custom" {
		t.Fatalf("out=%q", out)
	}
}

func TestNewWSAuth_TicketOnlyRejectsStrayAuthorization(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	inner := runtime.AuthFunc(func(ctx context.Context, r *http.Request) ([]byte, error) {
		t.Fatalf("inner must not be called when ticket-only receives a stray Authorization header (got %q)", r.Header.Get("Authorization"))
		return nil, nil
	})
	wrapped := runtime.NewWSAuth(runtime.WSAuthConfig{
		Inner:       inner,
		TicketStore: store,
		Modes:       []string{runtime.WSAuthModeTicket},
	})

	req := mustRequest(t, http.MethodGet, "/ws")
	req.Header.Set("Authorization", "Bearer leaked")
	_, err := wrapped(context.Background(), req)
	if !errors.Is(err, runtime.ErrWSAuthNoTicket) {
		t.Fatalf("want ErrWSAuthNoTicket, got %v", err)
	}
}

func TestNewWSAuth_HeaderPlusTicketFallsThrough(t *testing.T) {
	// header+ticket mode, neither present → Inner sees the raw request
	// and handles the 401 itself (preserving existing behaviour).
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	called := false
	inner := runtime.AuthFunc(func(ctx context.Context, r *http.Request) ([]byte, error) {
		called = true
		if r.Header.Get("Authorization") != "" {
			t.Fatalf("inner saw unexpected Authorization %q", r.Header.Get("Authorization"))
		}
		return nil, errors.New("missing credentials")
	})
	wrapped := runtime.NewWSAuth(runtime.WSAuthConfig{
		Inner:       inner,
		TicketStore: store,
		Modes:       []string{runtime.WSAuthModeHeader, runtime.WSAuthModeTicket},
	})
	req := mustRequest(t, http.MethodGet, "/ws")
	if _, err := wrapped(context.Background(), req); err == nil {
		t.Fatal("expected inner's missing-credentials error")
	}
	if !called {
		t.Fatal("inner must be called so it can produce the 401")
	}
}

// TestWSAuthTicketPrincipal_RecordsHeader verifies the helper Principal
// that the generated main.go hands to the WS ticket issuer copies the
// caller's Authorization header into the ticket labels verbatim — which
// is exactly what NewWSAuth re-reads at redeem time via
// WSAuthTicketHeaderLabel.
func TestWSAuthTicketPrincipal_RecordsHeader(t *testing.T) {
	req := mustRequest(t, http.MethodPost, "/api/ws/ticket")
	req.Header.Set("Authorization", "Bearer abc")

	labels, err := runtime.WSAuthTicketPrincipal(req)
	if err != nil {
		t.Fatalf("principal: %v", err)
	}
	if labels[runtime.WSAuthTicketHeaderLabel] != "Bearer abc" {
		t.Fatalf("labels: %v", labels)
	}
}

func TestWSAuthTicketPrincipal_RejectsMissingHeader(t *testing.T) {
	req := mustRequest(t, http.MethodPost, "/api/ws/ticket")
	if _, err := runtime.WSAuthTicketPrincipal(req); !errors.Is(err, runtime.ErrWSAuthTicketNoHeader) {
		t.Fatalf("expected ErrWSAuthTicketNoHeader, got %v", err)
	}
}

// TestWSAuthTicketPrincipal_IntegratesWithNewWSAuth is the end-to-end
// contract the generated main.go depends on: issuer records the header
// via WSAuthTicketPrincipal, NewWSAuth redeems it and replays it into
// Inner. Exercising them together guards against drift between the
// label key the Principal writes and the one NewWSAuth reads.
func TestWSAuthTicketPrincipal_IntegratesWithNewWSAuth(t *testing.T) {
	store := events.NewMemoryTicketStore()
	t.Cleanup(store.Close)

	issueReq := mustRequest(t, http.MethodPost, "/api/ws/ticket")
	issueReq.Header.Set("Authorization", "Bearer roundtrip")
	labels, err := runtime.WSAuthTicketPrincipal(issueReq)
	if err != nil {
		t.Fatalf("principal: %v", err)
	}
	ticket, err := store.Issue(context.Background(), labels, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	inner := runtime.AuthFunc(func(_ context.Context, r *http.Request) ([]byte, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer roundtrip" {
			t.Fatalf("inner saw %q", got)
		}
		return nil, nil
	})
	wrapped := runtime.NewWSAuth(runtime.WSAuthConfig{
		Inner:       inner,
		TicketStore: store,
		Modes:       []string{runtime.WSAuthModeHeader, runtime.WSAuthModeTicket},
	})
	upgradeReq := mustRequest(t, http.MethodGet, "/ws?ticket="+ticket)
	if _, err := wrapped(context.Background(), upgradeReq); err != nil {
		t.Fatalf("wrapped: %v", err)
	}
}

func TestNewWSAuth_Panics(t *testing.T) {
	cases := []struct {
		name string
		cfg  runtime.WSAuthConfig
	}{
		{"nil inner", runtime.WSAuthConfig{}},
		{"unknown mode", runtime.WSAuthConfig{
			Inner: runtime.NoAuth(),
			Modes: []string{"cookie"},
		}},
		{"ticket mode without store", runtime.WSAuthConfig{
			Inner: runtime.NoAuth(),
			Modes: []string{runtime.WSAuthModeTicket},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			_ = runtime.NewWSAuth(tc.cfg)
		})
	}
}
