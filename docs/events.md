# Events (`protoc-gen-events-go`)

Schema-first event system for protobridge. Annotate proto messages as events; the plugin generates typed Go publishers, typed subscribers, an AsyncAPI 3.0 schema, and an optional WebSocket broadcast endpoint that any HTTP router can mount.

```
   gRPC handler                                            Browser / SSE / external consumer
        │                                                            ▲
        │ EmitOrderCreated(ctx, bus, ev)                             │ JSON {"subject": ..., "event": ...}
        ▼                                                            │
  ┌──────────────┐         ┌──────────────────┐         ┌────────────┴────────────┐
  │ runtime.Bus  │── pub ──│ NATS / Redis /   │── sub ──│  events.NewBroadcast    │
  │ (Watermill)  │         │ RabbitMQ / Kafka │         │  Handler (WS upgrade)   │
  └──────────────┘         └──────────────────┘         └─────────────────────────┘
        ▲
        │ SubscribeOrderShipmentRequested(bus, "shipping", h)
        │
   Worker / sidecar
```

## Annotations

```protobuf
import "protobridge/events.proto";

message OrderCreated {
  option (protobridge.event) = {
    kind: BROADCAST
    visibility: PUBLIC
  };
  string order_id = 1;
  int64  total_cents = 2;
}

message OrderShipmentRequested {
  option (protobridge.event) = {
    kind: DURABLE
    subject: "shipments.requested"
    durable_group: "shipping-worker"
  };
  string order_id = 1;
}

message OrderShipped {
  option (protobridge.event) = {
    kind: BOTH               // worker queue + UI notification
    visibility: PUBLIC
  };
  string order_id = 1;
  string tracking_code = 2;
}
```

| Field | Default | Purpose |
|---|---|---|
| `subject` | `snake_case(message_name)` | Bus subject / topic / routing key. Exact match; no wildcards. |
| `kind` | _required_ | `BROADCAST` (best-effort fan-out) / `DURABLE` (at-least-once queue) / `BOTH` (durable first, then broadcast). |
| `durable_group` | `snake_case(message_name)` | Consumer group for `DURABLE` / `BOTH`. Subscribers in the same group split the stream. |
| `visibility` | `PUBLIC` | `PUBLIC` reaches the WS broadcast endpoint; `INTERNAL` is excluded from it. |
| `description` | — | Human-readable, surfaces in AsyncAPI. |

## Generated symbols

For each annotated message the plugin emits, into `<pkg-leaf>_events.go` next to the proto's `.pb.go`:

- `Subject<MessageName>` — string constant matching the resolved subject.
- `Emit<MessageName>(ctx, bus, ev)` — marshals the event and publishes it via the bus per the declared kind.
- `<MessageName>Handler` — typed function signature `func(ctx, *MessageType) error`.
- `Subscribe<MessageName>(bus, group, handler)` — load-balanced at-least-once subscriber. **Emitted only for `DURABLE` and `BOTH`.** Default group when caller passes `""` is the annotation's `durable_group` (or the subject if absent).
- `SubscribeBroadcast<MessageName>(bus, handler)` — ephemeral fan-out subscriber. **Emitted only for `BROADCAST` and `BOTH`.**

For each Go package that contains at least one PUBLIC fan-out event the plugin also emits `<pkg-leaf>_broadcast.go`:

- `<Pkg>BroadcastSubjects` — ordered slice of every PUBLIC fan-out subject in the package.
- `<Pkg>BroadcastEnvelope(subject, payload, headers) → []byte` — typed marshaler that decodes payload into the matching proto and re-encodes via `protojson` (UseProtoNames).
- `Register<Pkg>Broadcast(r chi.Router, bus events.Bus, prefix string)` — mounts a `GET <prefix>` WebSocket endpoint that streams every PUBLIC fan-out event in the package as `{"subject": "...", "event": {...}}`.

## Running the plugin

```bash
protoc \
  --events-go_out=./gen \
  -I . -I path/to/protobridge/proto \
  your/events.proto
```

Output layout:

```
gen/
├── <pkg-leaf>_events.go      # Emit*/Subscribe* helpers
├── <pkg-leaf>_broadcast.go   # WS broadcast handler (when any PUBLIC fan-out event exists)
└── schema/
    └── asyncapi.json         # AsyncAPI 3.0 contract for downstream client codegen
```

The generated files use the package name `events` by default. Override via `--events-go_opt=output_pkg=mypkg`.

## Runtime: choosing a bus

Pass any [Watermill](https://watermill.io)-backed implementation. Watermill supports NATS (Core + JetStream), Redis (Pub/Sub + Streams), RabbitMQ, Kafka, GCP Pub/Sub, AWS SQS/SNS, in-memory Go channels, and more — protobridge wraps the `Publisher`/`Subscriber` pair via `runtime/events.WatermillBus`.

```go
import (
    "github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"
    "github.com/mrs1lentcz/protobridge/runtime/events"
)

natsPub, _  := nats.NewPublisher(nats.PublisherConfig{ /* ... */ })
natsSub, _  := nats.NewSubscriber(nats.SubscriberConfig{ /* ... */ })

bus := &events.WatermillBus{
    BroadcastPublisher:  natsPub,
    BroadcastSubscriber: natsSub,
    DurablePublisher:    natsPub, // or a separate JetStream-backed publisher
    DurableSubscriber:   natsSub,
}
defer bus.Close()
```

For tests + dev:

```go
bus := events.NewInMemoryBus() // gochannel under the hood; no network
defer bus.Close()
```

## Emitting events

```go
import myevents "example.com/myapp/gen/events"

func (s *OrderServer) CreateOrder(ctx context.Context, req *pb.CreateOrderRequest) (*pb.CreateOrderResponse, error) {
    order := s.repo.Create(req)
    if err := myevents.EmitOrderCreated(ctx, s.bus, &myevents.OrderCreated{
        OrderId:    order.ID,
        TotalCents: order.TotalCents,
    }); err != nil {
        // BROADCAST: never errors (best-effort, logged on failure).
        // DURABLE / BOTH: error means the durable leg failed; surface it.
        return nil, err
    }
    return &pb.CreateOrderResponse{OrderId: order.ID}, nil
}
```

## Subscribing

```go
sub, err := myevents.SubscribeOrderShipmentRequested(bus, "shipping-worker",
    func(ctx context.Context, ev *myevents.OrderShipmentRequested) error {
        return shipping.Process(ctx, ev.OrderId)
    },
)
if err != nil { /* ... */ }
defer sub.Unsubscribe()
```

The handler returning a non-nil error nacks the message; the backend's redelivery policy decides what happens next.

## Broadcast WebSocket endpoint

```go
import (
    "github.com/go-chi/chi/v5"
    myevents "example.com/myapp/gen/events"
)

r := chi.NewRouter()
r.Use(authMiddleware) // your existing auth, applied to /events/* like any other route
myevents.RegisterMyappBroadcast(r, bus, "/events/myapp")
http.ListenAndServe(":8080", r)
```

A browser client opens `ws://api/events/myapp` and receives every PUBLIC fan-out event in the `myapp` package as it happens:

```json
{"subject": "order_created", "event": {"order_id": "o-1", "total_cents": 12000}}
{"subject": "order_shipped", "event": {"order_id": "o-1", "tracking_code": "TRK-1"}}
```

The wire format is JSON text frames; the `event` value matches the `payload` schema in the AsyncAPI document. INTERNAL events are filtered out at code-gen time and never appear on this endpoint.

## Multi-tenant routing with labels

Labels are key/value pairs (`map[string]string`) carried alongside every event and resolved per-connection on the broadcast WS endpoint. The model is deliberately the same one Kubernetes uses for `metadata.labels` + `labelSelector`: you tag the event with what's true about it, you tell each connection what's true about its principal, the runtime forwards an event to a connection only when every event-label key matches.

```protobuf
message OrderCreated {
  option (protobridge.event) = { kind: BOTH visibility: PUBLIC };
  string order_id = 1;
}
```

The annotation doesn't change. Labels are an orthogonal axis, applied at publish time:

```go
import "github.com/mrs1lentcz/protobridge/runtime/events"

func (s *OrderServer) CreateOrder(ctx context.Context, req *pb.CreateOrderRequest) (*pb.CreateOrderResponse, error) {
    order := s.repo.Create(req)

    // Stash labels on ctx once near the call boundary — typically in a
    // gRPC interceptor that derives them from the authenticated principal.
    // Every Emit* in the handler chain forwards them automatically.
    ctx = events.WithLabels(ctx,
        "tenant_id", order.TenantID,
        "project_id", order.ProjectID,
    )

    if err := myevents.EmitOrderCreated(ctx, s.bus, &myevents.OrderCreated{
        OrderId: order.ID,
    }); err != nil {
        return nil, err
    }
    return &pb.CreateOrderResponse{OrderId: order.ID}, nil
}
```

On the receiving side, `Register<Pkg>Broadcast` accepts a `BroadcastConfig` override that wires the auth-derived principal labels:

```go
myevents.RegisterMyappBroadcast(r, bus, "/events/myapp", events.BroadcastConfig{
    // Pull the principal's labels off the auth context populated by an
    // upstream middleware. Returning an error closes the connection
    // before the WS upgrade with a regular HTTP 401.
    PrincipalLabels: func(r *http.Request) (map[string]string, error) {
        principal := authctx.From(r.Context())
        if principal == nil {
            return nil, fmt.Errorf("not authenticated")
        }
        return map[string]string{
            "tenant_id":  principal.TenantID,
            "project_id": principal.ProjectID,
        }, nil
    },
    // Optional: defaults to DefaultLabelMatcher (K8s label-selector
    // semantics — every event-label key must match the principal).
    // Override to model "admin sees everything" or other custom hierarchies.
    Matcher: events.DefaultLabelMatcher,

    // Restrict browser origins (defaults to same-origin only).
    OriginPatterns: []string{"app.example.com"},
})
```

### Two filtering layers, one wire format

The runtime applies **server-side filtering** as the security boundary — a connection authenticated as `tenant_id=abc` never sees events tagged `tenant_id=xyz`, no matter what the client requests.

The wire envelope **carries the labels** so the browser can apply a second, UX-driven filter on top: "the user is currently looking at project xyz, drop everything else". This isn't security — it's just rendering economy.

```json
{
  "subject": "order_created",
  "labels": {
    "tenant_id":  "abc",
    "project_id": "xyz"
  },
  "event": { "order_id": "o-1" }
}
```

```js
const ws = new WebSocket("/events/myapp");
let currentProject = "xyz";

ws.onmessage = (e) => {
  const env = JSON.parse(e.data);
  // `labels` is omitempty on the wire — unlabeled / legacy events omit
  // the field entirely. Default to {} so the UX filter reads a missing
  // label as "no match" instead of throwing on undefined.
  const labels = env.labels || {};
  // Server already filtered by tenant_id; this is the per-screen UX filter.
  if (labels.project_id !== currentProject) return;
  renderEvent(env.subject, env.event);
};
```

Switch projects with a JS variable assignment — no reconnect, no server roundtrip.

### Backend-level subject scoping (advanced)

For NATS-only deployments at high scale, encoding labels into the subject (`tenant.{id}.orders.created`) lets the broker do the routing instead of the proxy holding every connection in memory. protobridge's generic broadcast handler doesn't do this — the in-process matcher is the cross-backend baseline. Wire your own subscriber against the bus directly when you want backend-native subject patterns.

## Failure semantics

| Kind | Publish error | Subscriber error |
|---|---|---|
| `BROADCAST` | Never (logged via the configured logger). | Logged + `Nack`'d (no-op for backends without redelivery). |
| `DURABLE` | Surfaced to the caller. | `Nack`'d → backend's redelivery policy applies. |
| `BOTH` | Durable error surfaces; broadcast leg is best-effort. | Per-leg independently. Subscribers must be **idempotent** — they may see the same event from both paths. |

Ordering: protobridge guarantees only what the underlying backend guarantees for the subject. Cross-subject ordering is never guaranteed.

## Schema artifact: AsyncAPI 3.0

`gen/schema/asyncapi.json` is the canonical machine-readable contract. Feed it to AsyncAPI's [generator](https://www.asyncapi.com/tools/generator) for typed clients in TypeScript, Python, Java, etc. — the same way `openrpc.json` works for the MCP plugin.

```bash
npx -p @asyncapi/cli asyncapi generate fromTemplate \
  gen/schema/asyncapi.json @asyncapi/typescript-nats-template \
  -o ./events-client
```

## Architecture rationale

- **Why Watermill?** Production-ready transport layer with 10+ backends, mature reconnect/retry handling, OpenTelemetry hooks. Writing four backends from scratch would cost ~6 weeks; the wrapper is ~150 LOC.
- **Why no auto-proto for the broadcast service?** Cross-plugin proto generation forces a two-pass build (events plugin emits proto → user re-runs protoc). Emitting a Go HTTP handler directly is a single pass and reuses any chi-compatible auth pipeline.
- **Why JSON envelope on the wire?** Frontends consume JSON natively; protojson preserves enum aliases (`x_var_name`) so the same JSON shape works for REST, MCP, and broadcast clients. Binary proto frames are an option for v0.5+ if a real bandwidth case shows up.
- **Why `BOTH` puts durable first?** Lossy broadcast is acceptable (UI eventually catches up via REST refresh); lossy durable is not (work coordination breaks). Doing durable first means a publish either fully succeeds at the durability layer or surfaces an error before the broadcast leg starts.
