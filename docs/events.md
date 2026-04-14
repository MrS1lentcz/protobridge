# Events (`protoc-gen-events-go`)

Schema-first event system for protobridge. Annotate proto messages as events; the plugin generates typed Go publishers + subscribers, an AsyncAPI 3.0 schema, and (when you declare a `(protobridge.broadcast)` service) a backend bus→stream adapter plus a gateway-side WebSocket fan-out hub.

```
   gRPC handler                         Backend gRPC server                 Gateway pod                    Browser
        │                                       │                               │                            ▲
        │ EmitOrderCreated(ctx, bus, ev)        │                               │                            │
        ▼                                       │                               │                            │
  ┌──────────────┐    ┌────────────────────┐    │  ┌──────────────────────┐     │     ┌──────────────────┐   │ JSON
  │ runtime.Bus  │──►│ Watermill backend  │   ───►│ NewXxxBroadcastServer │── stream ─►│ NewBroadcastHub  │──ws─┤
  │ (Watermill)  │    │ (NATS / in-mem /…) │    │  │ (subscribes on bus,  │     │     │ (1 src goroutine,│   │
  └──────────────┘    └────────────────────┘    │  │  packs envelope)     │     │     │  N WS clients,   │   │
        ▲                                       │  └──────────────────────┘     │     │  per-client filt)│   │
        │                                       │            ▲                  │     └──────────────────┘   │
        │ SubscribeOrderShipmentRequested(...)  │            │ pb.Register…     │                            │
        │                                       │            │                  │                            │
   Worker / sidecar                             │   user code in main.go        │                            │
                                                                                              labels filter
```

The bus is **internal to the backend process** — the gateway never connects to NATS/Redis/Kafka. All BE→FE traffic flows over a single long-lived gRPC server-stream per broadcast service per gateway pod.

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

## Declaring a broadcast service

To stream PUBLIC events to the browser, declare a `(protobridge.broadcast)` service alongside your event messages. The service must contain exactly one server-streaming RPC taking `google.protobuf.Empty` and returning a oneof envelope:

```protobuf
import "google/protobuf/empty.proto";

service OrderBroadcast {
  option (protobridge.broadcast) = { route: "/api/events/orders" };
  rpc Stream(google.protobuf.Empty) returns (stream OrderBroadcastEnvelope);
}

message OrderBroadcastEnvelope {
  // Optional. When present, populated by the backend adapter from publish
  // headers (events.WithLabels) and used by the gateway hub for per-principal
  // filtering. Forwarded into the JSON envelope sent to WS clients.
  map<string, string> labels = 1;

  oneof event {
    OrderCreated  order_created  = 2;
    OrderShipped  order_shipped  = 3;
  }
}
```

Constraints (enforced at codegen time, with clear errors):

- Exactly one method on the service.
- Method must be server-streaming.
- Input must be `google.protobuf.Empty` — the stream is a parameterless system fan-out.
- Envelope contains **only** the oneof variants and an optional `labels` field. Any other field rejected.
- Every oneof variant must point to a `(protobridge.event)`-annotated message with `visibility: PUBLIC`.

## Generated symbols

For each annotated message the plugin emits, into `<pkg-leaf>_events.go` next to the proto's `.pb.go`:

- `Subject<MessageName>` — string constant matching the resolved subject.
- `Emit<MessageName>(ctx, bus, ev)` — marshals the event and publishes it via the bus per the declared kind. Labels stashed on `ctx` via `events.WithLabels` are forwarded as message headers.
- `<MessageName>Handler` — typed function signature `func(ctx, *MessageType) error`.
- `Subscribe<MessageName>(bus, group, handler)` — load-balanced at-least-once subscriber. Emitted only for `DURABLE` and `BOTH`.
- `SubscribeBroadcast<MessageName>(bus, handler)` — ephemeral fan-out subscriber. Emitted only for `BROADCAST` and `BOTH`.

For each `(protobridge.broadcast)` service the plugin emits `<svc-snake>_broadcast.go`:

- `<Svc>Route`, `<Svc>Subjects` — wire constants exported for callers that mount manually.
- `<Svc>Envelope(subject, payload, labels) []byte` — typed marshaler that decodes payload into the matching proto and re-encodes via `protojson` (UseProtoNames), wrapped in the JSON envelope.
- `New<Svc>Source(conn) *<Svc>Source` — gateway-side `events.BroadcastSource` that opens the gRPC stream and emits one `BroadcastFrame` per envelope received. Implements drop-on-unknown-variant for forward compatibility.
- `New<Svc>Server(bus events.Bus) *<Svc>Server` — backend-side adapter implementing the streaming RPC. Subscribes to every subject on the bus and forwards each event into the stream as a typed envelope (with labels populated from publish headers).
- `Register<Svc>Broadcast(ctx, r, conn, prefix, extra...)` — one-call wireup that constructs a `BroadcastHub` against the gRPC stream and mounts it at `prefix`.

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
├── <pkg-leaf>_events.go        # Emit*/Subscribe* helpers (per Go package)
├── <svc-snake>_broadcast.go    # Source + Server + Register helper (per (protobridge.broadcast) service)
└── schema/
    └── asyncapi.json           # AsyncAPI 3.0 contract for downstream client codegen
```

The generated files use the package name `events` by default. Override via `--events-go_opt=output_pkg=mypkg`.

## Backend: choosing a bus

The bus is **only used inside the backend process** — your gRPC service handlers publish, your generated `<Svc>Server` adapter subscribes. The gateway never sees it. So the bus configuration is a private implementation detail you can change without touching the gateway.

Pass any [Watermill](https://watermill.io)-backed implementation. Watermill supports NATS (Core + JetStream), Redis (Pub/Sub + Streams), RabbitMQ, Kafka, GCP Pub/Sub, AWS SQS/SNS, in-memory Go channels, and more — protobridge wraps the `Publisher`/`Subscriber` pair via `runtime/events.WatermillBus`.

```go
import (
    "github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"
    "github.com/mrs1lentcz/protobridge/runtime/events"
)

natsPub, _ := nats.NewPublisher(nats.PublisherConfig{ /* ... */ })
natsSub, _ := nats.NewSubscriber(nats.SubscriberConfig{ /* ... */ })

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

## Backend: emitting events + serving the broadcast stream

```go
import (
    pb       "example.com/myapp/gen/grpc/myapp/v1"
    myevents "example.com/myapp/gen/events"
    "github.com/mrs1lentcz/protobridge/runtime/events"
)

func main() {
    bus := events.NewInMemoryBus()
    defer bus.Close()

    grpcSrv := grpc.NewServer()
    pb.RegisterOrderServiceServer(grpcSrv, &orderServer{bus: bus})

    // Generated bus → gRPC stream adapter. Register it like any other gRPC
    // service. One subscription per subject is opened per active gateway
    // stream; the adapter packs each event into the typed envelope (with
    // labels from publish headers) before sending.
    pb.RegisterOrderBroadcastServer(grpcSrv, myevents.NewOrderBroadcastServer(bus))

    grpcSrv.Serve(lis)
}

func (s *orderServer) CreateOrder(ctx context.Context, req *pb.CreateOrderRequest) (*pb.CreateOrderResponse, error) {
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

## Backend: subscribing to durable events

```go
sub, err := myevents.SubscribeOrderShipmentRequested(bus, "shipping-worker",
    func(ctx context.Context, ev *myevents.OrderShipmentRequested) error {
        return shipping.Process(ctx, ev.OrderId)
    },
)
if err != nil { /* ... */ }
defer sub.Unsubscribe()
```

Returning a non-nil error nacks the message; the backend's redelivery policy decides what happens next.

## Gateway: mounting the broadcast endpoint

The generated `protoc-gen-protobridge` main.go does this automatically — for every `(protobridge.broadcast)` service it dials a long-lived gRPC client connection and registers the WS endpoint:

```go
// Generated. Per broadcast service:
broadcastCtx, broadcastCancel := context.WithCancel(context.Background())
defer broadcastCancel()

orderBroadcastConn, err := grpc.NewClient(orderBroadcastAddr, dialOpts(...)...)
defer orderBroadcastConn.Close()

myevents.RegisterOrderBroadcastBroadcast(broadcastCtx, r, orderBroadcastConn, "/api/events/orders",
    events.BroadcastConfig{PrincipalLabels: principalLabelsFn},
)
```

Each broadcast service requires its own `PROTOBRIDGE_<SCREAMING_SVC_NAME>_ADDR` env var pointing to the backend gRPC server. The gateway opens **one** stream per service per pod; all WS clients share that stream via the in-process hub.

If you mount the hub yourself (custom router, manual lifecycle), the wire is symmetric:

```go
hub := events.NewBroadcastHub(ctx, events.BroadcastConfig{
    Source:  myevents.NewOrderBroadcastSource(conn),
    Marshal: myevents.OrderBroadcastEnvelope,
    PrincipalLabels: func(r *http.Request) (map[string]string, error) { /* … */ },
})
r.Method(http.MethodGet, "/api/events/orders", hub)
```

A browser opens `ws://api/events/orders` and receives every PUBLIC fan-out event in the service as it happens:

```json
{"subject": "order_created", "labels": {"tenant_id": "abc"}, "event": {"order_id": "o-1", "total_cents": 12000}}
{"subject": "order_shipped", "labels": {"tenant_id": "abc"}, "event": {"order_id": "o-1", "tracking_code": "TRK-1"}}
```

INTERNAL events are filtered out at code-gen time and never appear here.

## Multi-tenant routing with labels

Labels are key/value pairs (`map[string]string`) carried alongside every event and resolved per-connection at the gateway hub. The model is deliberately the same one Kubernetes uses for `metadata.labels` + `labelSelector`: tag the event with what's true about it, tell each connection what's true about its principal, the hub forwards an event to a connection only when every event-label key matches.

Labels travel three hops:

1. **Backend publish** — `events.WithLabels(ctx, k, v)` stashes them on the context; the generated `Emit*` helper writes them as bus message headers.
2. **Backend → gateway gRPC stream** — the `New<Svc>Server` adapter reads `m.Headers` via `events.HeadersToLabels` and writes them into the typed envelope's `labels` field.
3. **Gateway → browser** — the hub forwards them in the JSON envelope's `labels` field for client-side UX filtering.

Backend side stays unchanged from the bus-only model:

```go
ctx = events.WithLabels(ctx, "tenant_id", order.TenantID, "project_id", order.ProjectID)
myevents.EmitOrderCreated(ctx, s.bus, &myevents.OrderCreated{OrderId: order.ID})
```

Gateway hub pulls the principal's labels off the auth-decorated request:

```go
events.BroadcastConfig{
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
    Matcher: events.DefaultLabelMatcher,

    // Restrict browser origins (defaults to same-origin only).
    OriginPatterns: []string{"app.example.com"},

    // Per-WS-client outbound queue depth. When a slow client fills it, the
    // hub drops the oldest pending frame and continues — UX semantics, the
    // newest nudge is the most useful one. Defaults to 64.
    ClientBuffer: 128,
}
```

When `protoc-gen-protobridge` detects a `labels` field on the auth method's response, it wires `PrincipalLabels` automatically; you usually don't write this by hand.

### Two filtering layers, one wire format

The hub applies **server-side filtering** as the security boundary — a connection authenticated as `tenant_id=abc` never sees events tagged `tenant_id=xyz`, no matter what the client requests.

The wire envelope **carries the labels** so the browser can apply a second, UX-driven filter on top: "the user is currently looking at project xyz, drop everything else". This isn't security — just rendering economy.

```json
{
  "subject": "order_created",
  "labels": { "tenant_id": "abc", "project_id": "xyz" },
  "event":  { "order_id": "o-1" }
}
```

```js
const ws = new WebSocket("/events/myapp");
let currentProject = "xyz";

ws.onmessage = (e) => {
  const env = JSON.parse(e.data);
  // `labels` is omitempty on the wire — unlabeled / legacy events omit
  // the field entirely.
  const labels = env.labels || {};
  // Server already filtered by tenant_id; this is the per-screen UX filter.
  if (labels.project_id !== currentProject) return;
  renderEvent(env.subject, env.event);
};
```

Switch projects with a JS variable assignment — no reconnect, no server roundtrip.

## Failure semantics

| Kind | Publish error | Subscriber error |
|---|---|---|
| `BROADCAST` | Never (logged via the configured logger). | Logged + `Nack`'d (no-op for backends without redelivery). |
| `DURABLE` | Surfaced to the caller. | `Nack`'d → backend's redelivery policy applies. |
| `BOTH` | Durable error surfaces; broadcast leg is best-effort. | Per-leg independently. Subscribers must be **idempotent** — they may see the same event from both paths. |

Gateway hub failure modes:

- **Source dies (gRPC stream errors out)**: hub logs, current clients keep their WS open but stop receiving frames. New clients can still attach. The hub does not auto-reconnect inside `Run` — wrap the source in a retry loop if you need that.
- **Slow WS client**: per-client bounded queue with drop-oldest. Drop count logged on disconnect. Other clients on the same hub are unaffected.
- **WS write timeout**: each write is bounded to 10s — a stuck TCP socket can't pin a goroutine indefinitely.

Ordering: protobridge guarantees only what the underlying backend guarantees for the subject. Cross-subject ordering is never guaranteed.

## Schema artifact: AsyncAPI 3.0

`gen/schema/asyncapi.json` is the canonical machine-readable contract. Feed it to AsyncAPI's [generator](https://www.asyncapi.com/tools/generator) for typed clients in TypeScript, Python, Java, etc.

```bash
npx -p @asyncapi/cli asyncapi generate fromTemplate \
  gen/schema/asyncapi.json @asyncapi/typescript-nats-template \
  -o ./events-client
```

## Architecture rationale

- **Why Watermill?** Production-ready transport layer with 10+ backends, mature reconnect/retry handling, OpenTelemetry hooks. Writing four backends from scratch would cost ~6 weeks; the wrapper is ~150 LOC.
- **Why a gRPC stream between backend and gateway, not a shared bus?** Keeps the bus a private implementation detail of the backend — gateway has no NATS credentials, no network policy to messaging infra, no awareness of subject names. Gateway stays a thin HTTP↔gRPC proxy. Switching the backend bus from in-memory to NATS to Redis requires zero gateway changes.
- **Why one shared stream per gateway pod, not one per WS client?** Every WS client on the same pod sees the same logical stream, differing only by per-principal label filter (which runs locally in the hub). Opening N streams to the backend would multiply backend egress N× without information gain. At scale the math: 10k WS × 5 pods × 3 services × 1 stream = **15** backend streams instead of 150 000.
- **Why drop-oldest on slow clients?** UX nudges — a stale "something changed" event is worth less than the latest one. Drop-oldest also isolates blast radius: one slow client never blocks the shared stream for everyone else on the pod.
- **Why `google.protobuf.Empty` as the only allowed input?** Broadcast streams are parameterless system fan-outs. Per-client parameters (filters, search predicates) belong on user-defined custom streaming RPCs — `(protobridge.broadcast)` is specifically for the "all PUBLIC events from this service" channel. Forcing Empty avoids plumbing arbitrary input proto packages through the codegen.
- **Why JSON envelope on the wire?** Frontends consume JSON natively; protojson preserves enum aliases (`x_var_name`) so the same JSON shape works for REST, MCP, and broadcast clients.
- **Why `BOTH` puts durable first?** Lossy broadcast is acceptable (UI eventually catches up via REST refresh); lossy durable is not (work coordination breaks). Doing durable first means a publish either fully succeeds at the durability layer or surfaces an error before the broadcast leg starts.
