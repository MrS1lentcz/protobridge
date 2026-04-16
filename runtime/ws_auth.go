package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/mrs1lentcz/protobridge/runtime/events"
)

// WSAuthMode enumerates the auth sources recognised by NewWSAuth. String
// constants rather than an iota so misconfiguration surfaces at wiring
// time via a clear error.
const (
	WSAuthModeHeader = "header"
	WSAuthModeTicket = "ticket"
)

// WSAuthTicketHeaderLabel is the reserved ticket label key that the
// issuer's Principal function MUST populate with the original
// Authorization header. NewWSAuth reads this exact key when redeeming a
// ticket and replays it as an Authorization header so the wrapped
// AuthFunc sees the same request shape it would for a header-auth
// upgrade. Fixed on purpose — a configurable name is more footgun than
// flexibility.
const WSAuthTicketHeaderLabel = "authorization"

// WSAuthConfig configures NewWSAuth.
type WSAuthConfig struct {
	// Inner is the upstream AuthFunc that calls the AuthService. Required.
	// NewWSAuth decorates it: when a ticket is redeemed, the replayed
	// Authorization header is written into a cloned *http.Request before
	// Inner is called, so Inner's view of the request is indistinguishable
	// from a direct header-authenticated upgrade.
	Inner AuthFunc

	// TicketStore redeems the one-shot tickets issued by
	// events.NewTicketIssuer. Required when Modes contains "ticket".
	TicketStore events.TicketStore

	// TicketParam is the query-string key carrying the ticket on the WS
	// handshake URL. Defaults to "ticket".
	TicketParam string

	// Modes restricts which auth sources are accepted. When empty, only
	// the "header" source is enabled — callers opt into ticket redemption
	// explicitly. Unknown values panic at NewWSAuth time so typos are
	// caught before the server starts serving.
	Modes []string
}

// ErrWSAuthNoTicket is returned when Modes is ticket-only but the
// request carries no ticket in the configured query parameter. Exposed
// as a sentinel so callers can distinguish "client forgot the ticket"
// (a valid 401) from a TicketStore transport failure.
var ErrWSAuthNoTicket = errors.New("runtime: ticket required but not supplied")

// NewWSAuth returns an AuthFunc that layers ticket-based auth on top of
// an existing header-auth function. The browser WebSocket constructor
// can't set custom headers, so services that accept browser upgrades
// issue a short-lived ticket over a normal authenticated POST and then
// accept ?ticket=<t> on the WS handshake URL.
//
// Modes acts as an allow-list:
//
//   - "header"          → Authorization passed through to Inner. No ticket
//                         redemption even if ?ticket=<t> is present.
//   - "ticket"          → only ticket redemption is honoured; requests
//                         without a ticket fail fast with ErrWSAuthNoTicket
//                         so a stray Authorization header cannot be
//                         forwarded to Inner behind the caller's back.
//   - "header,ticket"   → Authorization wins when set; otherwise a ticket
//                         is redeemed and its labels[WSAuthTicketHeaderLabel]
//                         value is replayed as Authorization on a cloned
//                         request before Inner is invoked.
//
// When both sources are accepted but neither is present, the request is
// forwarded to Inner unchanged so Inner's normal "missing credentials"
// branch produces the 401.
//
// Panics when Inner is nil, Modes references an unknown source, or
// ticket mode is requested without a TicketStore — config errors that
// should surface at startup, not on the first WS upgrade.
func NewWSAuth(cfg WSAuthConfig) AuthFunc {
	if cfg.Inner == nil {
		panic("runtime: WSAuthConfig.Inner is required")
	}
	modes := cfg.Modes
	if len(modes) == 0 {
		modes = []string{WSAuthModeHeader}
	}
	acceptHeader, acceptTicket := false, false
	for _, m := range modes {
		switch m {
		case WSAuthModeHeader:
			acceptHeader = true
		case WSAuthModeTicket:
			acceptTicket = true
		default:
			panic(fmt.Sprintf("runtime: unknown WSAuth mode %q", m))
		}
	}
	if acceptTicket && cfg.TicketStore == nil {
		panic("runtime: WSAuthConfig.TicketStore is required when Modes includes \"ticket\"")
	}
	param := cfg.TicketParam
	if param == "" {
		param = "ticket"
	}

	return func(ctx context.Context, r *http.Request) ([]byte, error) {
		if acceptHeader && r.Header.Get("Authorization") != "" {
			return cfg.Inner(ctx, r)
		}
		if acceptTicket {
			ticket := r.URL.Query().Get(param)
			if ticket == "" {
				if !acceptHeader {
					// Ticket-only: we must not forward the raw request
					// because Inner would see the (disallowed) Authorization
					// header if one happens to be set.
					return nil, ErrWSAuthNoTicket
				}
				// Header+ticket with neither present → let Inner's own
				// missing-credentials path produce the 401.
				return cfg.Inner(ctx, r)
			}
			labels, err := cfg.TicketStore.Redeem(ctx, ticket)
			if err != nil {
				return nil, err
			}
			auth := labels[WSAuthTicketHeaderLabel]
			if auth == "" {
				return nil, errors.New("runtime: ticket labels missing authorization")
			}
			// r.Clone already deep-copies the header map, so we only set
			// the replayed key on the clone's Header — no need to re-clone.
			replayed := r.Clone(ctx)
			replayed.Header.Set("Authorization", auth)
			return cfg.Inner(ctx, replayed)
		}
		return cfg.Inner(ctx, r)
	}
}
