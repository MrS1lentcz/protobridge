# protobridge

[![CI](https://github.com/MrS1lentcz/protobridge/actions/workflows/ci.yml/badge.svg)](https://github.com/MrS1lentcz/protobridge/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/MrS1lentcz/protobridge/branch/main/graph/badge.svg)](https://codecov.io/gh/MrS1lentcz/protobridge)
[![Go Reference](https://pkg.go.dev/badge/github.com/mrs1lentcz/protobridge.svg)](https://pkg.go.dev/github.com/mrs1lentcz/protobridge)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Multi-protocol gateway generator for gRPC services. Write your business logic once as a gRPC server, then generate proxies that expose it as REST + WebSocket + SSE (`protoc-gen-protobridge`) and as MCP tools (`protoc-gen-mcp`) — all driven by `.proto` annotations, no handwritten glue code.

```
                  ┌──────────────┐
                  │  REST proxy  │── HTTP / WS / SSE  ─┐
                  └──────────────┘                     │
                                                       ├──→  ┌──────────────┐
                  ┌──────────────┐                     │     │ gRPC backend │
                  │   MCP proxy  │── stdio / HTTP  ────┤     │  (your code) │
                  └──────────────┘                     │     └──────────────┘
                                                       │
                  (more proxies on the way) ───────────┘
```

## Why protobridge

One implementation, many wire formats. The same gRPC service can be exposed to a browser (REST + OpenAPI), to an LLM agent (MCP tools), and to a backend consumer (raw gRPC) without rewriting business logic. Auth, metadata, observability, and validation share one pipeline — set up once at the gRPC layer, every proxy inherits it.

Compared to single-purpose gateways:

- **Broken `oneof` / union types** in other gateways — `protojson` produces flat objects with no discriminator. Frontend can't tell which variant it received. protobridge generates clean discriminated unions with a `"protobridge_disc"` field, validated for global uniqueness at generation time.
- **Unusable enums** elsewhere — proto enums expose raw `SCREAMING_CASE` names and the meaningless `0` default. protobridge strips the zero member entirely and lets you define clean names via `x_var_name` (`"low"` instead of `TASK_PRIORITY_LOW`). Aliases work for both input and output, in REST, MCP and OpenAPI.
- **No streaming story** in most gateways. protobridge automatically maps all stream types (server, client, bidi) to WebSocket or SSE with configurable connection modes (`private` per-user vs `broadcast` fan-out).
- **No MCP path at all** elsewhere. `protoc-gen-mcp` emits a stdio + streamable-HTTP MCP server with JSON Schema derived from your proto types and tool descriptions pulled from proto leading comments — drop a binary into Claude Desktop / Cursor / your custom MCP host.
- **No observability out of the box** elsewhere. protobridge generates OpenTelemetry integration day one: W3C trace propagation, Prometheus metrics, Sentry error reporting, automatic connection health monitoring with transparent retry.
- **Boilerplate everywhere** in alternatives — even with a gateway you still write middleware, auth wiring, connection management, `main.go`. protobridge generates all of it for every proxy, including Dockerfile and Kubernetes manifest.

## Performance

Benchmarked on Apple M1 Pro in Docker Desktop with strict resource limits. Full results in [`bench/results/benchmark.txt`](bench/results/benchmark.txt).

| Scenario | Concurrency | Throughput | Success | p50 | p99 |
|---|---|---|---|---|---|
| Unary POST (no auth) | 50 | **20,800 req/s** | 100% | 1.3ms | 41ms |
| Unary POST (with auth) | 50 | **12,000 req/s** | 100% | 2.3ms | 43ms |
| Unary POST (no auth) | 100 | **21,200 req/s** | 100% | 2.3ms | 48ms |
| Unary POST (no auth) | 500 | **19,300 req/s** | 100% | 14ms | 90ms |
| Unary POST (no auth) | 1,000 | **18,900 req/s** | 100% | 55ms | 163ms |
| Unary POST (no auth) | 5,000 | **15,900 req/s** | 100% | 294ms | 881ms |
| Unary POST (no auth) | 10,000 | **18,200 req/s** | 100% | 514ms | 1.48s |

**Resource usage** (proxy on 1 CPU, 2GB RAM limit):
- Peak CPU: **109%** (saturated single core under load)
- Peak RAM: **488 MiB** while holding 10,000 concurrent connections
- Avg RAM: **62 MiB** across the full run

The proxy sustains **zero errors up to 10,000 concurrent connections** on a single CPU core. Throughput plateaus around 19k req/s as CPU saturates; latency grows proportionally with queue depth. Auth adds ~40% overhead (two sequential gRPC calls per request). gRPC connections scale automatically from 1 to N based on load.

```bash
cd bench && make isolated   # run it yourself
```

## How it works

```
.proto files ──> protoc-gen-protobridge ──> Go source code + openapi.yaml
                                               │
                                               ▼
                                    go build ──> REST proxy binary
```

protobridge reads your annotated `.proto` files and generates:

- **HTTP handlers** for every RPC with a `google.api.http` annotation
- **WebSocket handlers** for streaming RPCs (server, client, bidirectional)
- **Authentication middleware** from a single annotated auth RPC
- **Request validation** for required fields and headers
- **gRPC error mapping** to standard HTTP status codes
- **Frontend-ready OpenAPI 3.1 spec** -- clean enum names, discriminated unions, no zero members
- **AsyncAPI 3.0 spec** for WebSocket/streaming endpoints
- **`main.go`** with connection pooling ([gox/grpcx](https://github.com/MrS1lentcz/gox)), Sentry integration, ENV-based configuration, and graceful shutdown
- **Dockerfile + Kubernetes manifest** ready for deployment

## Installation

```bash
go install github.com/mrs1lentcz/protobridge/cmd/protoc-gen-protobridge@latest  # REST + WS + SSE proxy
go install github.com/mrs1lentcz/protobridge/cmd/protoc-gen-mcp@latest          # MCP proxy
```

Requirements: `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`

## Quick start

**1. Import protobridge options and annotate your proto:**

```protobuf
import "protobridge/options.proto";
import "google/api/annotations.proto";

service TaskService {
  rpc CreateTask(CreateTaskRequest) returns (Task) {
    option (google.api.http) = {post: "/tasks"};
    option (protobridge.required_headers) = "user_id";
  }

  rpc GetTask(GetTaskRequest) returns (Task) {
    option (google.api.http) = {get: "/tasks/{task_id}"};
  }
}
```

**2. Run protoc:**

```bash
protoc \
  --protobridge_out=./gateway \
  --protobridge_opt=handler_pkg=your/module/gateway/handler \
  -I . -I path/to/protobridge/proto -I path/to/googleapis \
  your/service.proto
```

The `handler_pkg` option is the **Go import path** that the generated `main.go` uses to import the `handler/` subpackage. It must match where you put the output: if `--protobridge_out=./gateway` and your module is `github.com/you/myapp`, then `handler_pkg=github.com/you/myapp/gateway/handler`.

If you omit `handler_pkg`, the plugin walks up from the current directory to find `go.mod` and, if it sees a conventional output dir (`gen/protobridge/`, `protobridge/`, or `gen/`), uses that. Reproducible CI builds should pass it explicitly.

**3. Build and run the generated proxy:**

```bash
cd gateway
go build -o gateway .

PROTOBRIDGE_TASK_SERVICE_ADDR=localhost:50051 ./gateway
```

The proxy directory layout:

```
gateway/
├── main.go              # entry point — package main
├── handler/             # one file per service — package handler
│   └── task_service.go
├── Dockerfile
├── k8s.yaml
├── .env.example
└── schema/
    ├── openapi.yaml
    └── asyncapi.yaml    # only if you have streaming methods
```

The proxy is now listening on `:8080` and forwarding requests to your gRPC backend.

### Generate the MCP proxy too

Annotate the methods you want exposed to LLM clients:

```protobuf
import "protobridge/options.proto";

service ToolService {
  // Creates a task in the current project.
  rpc CreateTask(CreateTaskRequest) returns (Task) {
    option (protobridge.mcp) = true;
    option (protobridge.mcp_scope) = "chat session";
  }
}
```

Run the second plugin:

```bash
protoc \
  --mcp_out=./mcp \
  --mcp_opt=handler_pkg=your/module/mcp/handler \
  -I . -I path/to/protobridge/proto \
  your/service.proto

cd mcp && go build -o mcp-proxy .
PROTOBRIDGE_TOOL_SERVICE_ADDR=localhost:50051 SESSION_ID=$(uuidgen) ./mcp-proxy
```

By default the binary speaks **stdio** (Claude Desktop / Cursor / `mcp-cli`); set `PROTOBRIDGE_MCP_TRANSPORT=http` for streamable HTTP mode (`PROTOBRIDGE_MCP_HTTP_ADDR=:8081` to choose the port). Identity (e.g. `SESSION_ID`) is forwarded into gRPC metadata so the same backend handles REST and MCP requests with one auth pipeline.

See [`docs/mcp.md`](docs/mcp.md) for the full MCP guide and [`docs/rest.md`](docs/rest.md) for the REST plugin reference.

## Proto annotations

All API configuration lives in `.proto` files. No YAML, no config files.

### Field options

```protobuf
message CreateTaskRequest {
  string title = 1 [(protobridge.required) = true];
  TaskPriority priority = 2 [(protobridge.required) = true];
  string description = 3; // optional
}
```

Required fields are validated at the REST layer before the gRPC call. Missing or zero-value fields return `422` with a structured error listing all violations.

### Service options

```protobuf
service TaskService {
  // Human-readable group name for OpenAPI tags.
  // Defaults to the proto service name if not set.
  option (protobridge.display_name) = "Tasks";

  // Path prefix prepended to all HTTP paths in this service.
  // Useful for API versioning or grouping.
  option (protobridge.path_prefix) = "/api/v1";

  rpc CreateTask(...) returns (...) {
    option (google.api.http) = {post: "/tasks"};
    // Actual endpoint becomes: POST /api/v1/tasks
  }
}
```

`display_name` maps directly to OpenAPI tags, which tools like Swagger UI use to group endpoints. `path_prefix` is applied to every HTTP path in the service at generation time -- the proto `google.api.http` annotations stay clean while the generated API gets proper versioning/grouping.

### Method options

```protobuf
rpc CreateTask(CreateTaskRequest) returns (Task) {
  option (google.api.http) = {post: "/tasks"};

  // Extracted from HTTP headers, forwarded as gRPC metadata.
  option (protobridge.required_headers) = "user_id";
  option (protobridge.required_headers) = "org_id";

  // Map query params into a nested message field.
  option (protobridge.query_params_target) = "paging";

  // Skip authentication for this endpoint.
  option (protobridge.exclude_auth) = true;
}
```

### Enum options

Proto enums are notoriously painful for frontend consumers: the raw `SCREAMING_CASE` names are ugly, the `0` default leaks into responses, and codegen tools produce meaningless constants. protobridge fixes all of this.

```protobuf
enum TaskPriority {
  TASK_PRIORITY_UNSPECIFIED = 0;  // internal only -- never in REST or OpenAPI
  TASK_PRIORITY_LOW = 1 [(protobridge.x_var_name) = "low"];
  TASK_PRIORITY_MEDIUM = 2 [(protobridge.x_var_name) = "medium"];
  TASK_PRIORITY_HIGH = 3 [(protobridge.x_var_name) = "high"];
  TASK_PRIORITY_CRITICAL = 4 [(protobridge.x_var_name) = "critical"];
}
```

**What happens:**

- The `0` member is **never exposed** -- not in JSON responses, not in the OpenAPI spec, not as an accepted input value
- `x_var_name` overrides the enum name in JSON and OpenAPI: your frontend receives `"low"`, `"high"`, `"critical"` instead of `TASK_PRIORITY_LOW`
- **Optional** enum fields can be omitted from the request body (defaults to `0` internally for gRPC, invisible to the API consumer)
- **Required** enum fields must be non-zero -- the validation layer rejects `0` the same way it rejects an empty string

**Generated OpenAPI:**

```yaml
priority:
  type: string
  enum:
    - low
    - medium
    - high
    - critical
```

This is what frontend codegen tools (openapi-generator, orval, etc.) need to produce clean TypeScript types like `type TaskPriority = "low" | "medium" | "high" | "critical"`.

### Authentication

Annotate exactly one RPC as the auth method:

```protobuf
service AuthService {
  rpc Authenticate(AuthRequest) returns (AuthResponse) {
    option (protobridge.auth_method) = true;
  }
}

message AuthRequest {
  map<string, string> headers = 1;
}
```

This RPC is **not** exposed as a REST endpoint. On every incoming request, the proxy:
1. Calls the auth RPC with all HTTP headers
2. Serializes the response via `proto.Marshal` -> base64
3. Forwards it as gRPC metadata (`x-protobridge-user`) on every downstream call

Your backend services deserialize this metadata to get the authenticated user. See the [taskboard example](example/taskboard/server/main.go) for a working implementation.

## Request mapping

| Source | Destination | Configuration |
|---|---|---|
| Path parameters (`/tasks/{task_id}`) | gRPC metadata | `google.api.http` path template |
| HTTP headers | gRPC metadata | `protobridge.required_headers` |
| JSON body | Protobuf message | Automatic via `protojson` |
| Query parameters | Nested message field | `protobridge.query_params_target` |

### Path parameters

```
GET /tasks/abc123  →  gRPC metadata: task_id = "abc123"
```

Path parameters are forwarded as gRPC metadata, not injected into the request body. This is intentional -- path params are typically IDs and belong in metadata.

protobridge uses [chi](https://github.com/go-chi/chi) as the HTTP router. Path parameters support chi's full URL pattern syntax, including regex constraints:

```protobuf
// Basic parameter
option (google.api.http) = {get: "/tasks/{task_id}"};

// Regex constraint: only UUIDs
option (google.api.http) = {get: "/tasks/{task_id:[a-f0-9-]{36}}"};

// Numeric only
option (google.api.http) = {get: "/users/{user_id:[0-9]+}"};
```

### Query parameters

```
GET /tasks?paging.page=2&paging.limit=20
```

With `(protobridge.query_params_target) = "paging"`, query params are mapped into the `paging` nested message field. Field names must match exactly.

## WebSocket / Streaming

Streaming RPCs are automatically exposed as WebSocket endpoints:

| gRPC stream type | HTTP transport |
|---|---|
| Unary | Standard HTTP |
| Server streaming | WebSocket or SSE |
| Client streaming | WebSocket (client sends, server receives) |
| Bidirectional | WebSocket (full duplex) |

### Connection modes

Every streaming endpoint has a **ws_mode** that controls how connections are managed:

```protobuf
// Private: each WS client gets its own gRPC stream with user_id in metadata.
// Backend knows exactly who it's talking to.
rpc WatchTasks(WatchTasksRequest) returns (stream TaskEvent) {
  option (google.api.http) = {get: "/tasks/watch"};
  option (protobridge.ws_mode) = "private";
}

// Broadcast: all WS clients receive the same events. No per-user routing.
// Good for public feeds, market data, system-wide notifications.
rpc ActivityFeed(WatchTasksRequest) returns (stream TaskEvent) {
  option (google.api.http) = {get: "/tasks/feed"};
  option (protobridge.ws_mode) = "broadcast";
}
```

`private` and `broadcast` are independent of authentication -- a broadcast endpoint can still require auth (e.g. a shared dashboard visible only to logged-in users), and a private endpoint without auth makes no sense (the generator will warn).

### Server-Sent Events (SSE)

For server→client one-way streaming, SSE is lighter than WebSocket -- no upgrade handshake, works through HTTP/2 proxies, and has native browser support via `EventSource`.

```protobuf
rpc TaskNotifications(WatchTasksRequest) returns (stream TaskEvent) {
  option (google.api.http) = {get: "/tasks/notifications"};
  option (protobridge.sse) = true;
}
```

The `sse` option is only valid on **server-streaming** RPCs (not client or bidi). The generated handler uses `text/event-stream` with JSON payloads -- each gRPC message becomes one `data:` frame.

### AsyncAPI spec

Alongside `openapi.yaml`, protobridge generates an **`asyncapi.yaml`** (AsyncAPI 3.0) spec for all WebSocket and SSE endpoints. This lets frontend teams use AsyncAPI tooling for client codegen, documentation, and contract testing.

## Error handling

gRPC status codes are mapped to HTTP:

| gRPC code | HTTP status |
|---|---|
| `INVALID_ARGUMENT` | 400 |
| `UNAUTHENTICATED` | 401 |
| `PERMISSION_DENIED` | 403 |
| `NOT_FOUND` | 404 |
| `ALREADY_EXISTS` | 409 |
| `RESOURCE_EXHAUSTED` | 429 |
| `UNAVAILABLE` | 503 |
| Others | 500 |

Error response body:

```json
{
  "code": "INVALID_ARGUMENT",
  "message": "human readable message",
  "details": [
    {"field": "title", "reason": "required"}
  ]
}
```

Server-side errors (5xx) are automatically reported to Sentry.

## Observability

protobridge has built-in OpenTelemetry support for distributed tracing and Prometheus metrics.

### Distributed tracing

Every incoming HTTP request is automatically instrumented:
- If an upstream proxy (nginx, Envoy) sends a `traceparent` header (W3C TraceContext), protobridge continues the trace
- If no trace context arrives, a new root span is created
- Trace context is propagated to all downstream gRPC calls via `otelgrpc` client interceptor
- Spans include HTTP method, route, status code, and duration

Traces are exported via OTLP (gRPC) to any compatible collector (Jaeger, Tempo, Datadog, etc.).

### Prometheus metrics

When `PROTOBRIDGE_METRICS_PORT` is set, a separate HTTP server exposes `/metrics` in Prometheus format:
- `http_server_duration` – request latency histogram (method, route, status)
- `http_server_request_count` – request counter
- `protobridge.active_connections` – active WS/SSE connections gauge

### Connection health

gRPC connections are automatically monitored via `grpcx.Pool.EnableHealthWatch`. Unhealthy connections (TransientFailure/Shutdown) are reconnected transparently. Unary RPC calls retry once on transient gRPC errors (`Unavailable`, `Aborted`, `ResourceExhausted`).

## Environment variables

Generated from proto service names in `SCREAMING_SNAKE_CASE`:

```bash
# gRPC targets (required)
PROTOBRIDGE_TASK_SERVICE_ADDR=task-service:50051
PROTOBRIDGE_AUTH_SERVICE_ADDR=auth-service:50051

# HTTP server
PROTOBRIDGE_PORT=8080                           # default: 8080

# TLS (HTTPS)
PROTOBRIDGE_TLS_CERT=/certs/cert.pem            # optional, enables HTTPS
PROTOBRIDGE_TLS_KEY=/certs/key.pem              # optional, required with TLS_CERT
PROTOBRIDGE_TLS_SERVER_NAME=api.example.com     # optional, TLS server name
PROTOBRIDGE_TASK_SERVICE_TLS=true               # optional, per-service gRPC TLS

# CORS
PROTOBRIDGE_CORS_ORIGINS=https://app.example.com,https://admin.example.com  # default: *
PROTOBRIDGE_CORS_METHODS=GET,POST,PUT,DELETE     # default: GET,POST,PUT,DELETE,PATCH,OPTIONS
PROTOBRIDGE_CORS_HEADERS=Content-Type,Authorization,X-Request-ID  # default: Content-Type,Authorization
PROTOBRIDGE_CORS_MAX_AGE=3600                    # default: 86400 (seconds)

# Observability
PROTOBRIDGE_SENTRY_DSN=https://...@sentry.io/1  # optional
PROTOBRIDGE_OTEL_ENDPOINT=otel-collector:4317    # optional, OTLP gRPC endpoint
PROTOBRIDGE_OTEL_SERVICE_NAME=protobridge        # optional, default: "protobridge"
PROTOBRIDGE_METRICS_PORT=9090                    # optional, Prometheus /metrics

# gRPC client options (global)
PROTOBRIDGE_GRPC_OPTIONS=max_recv_msg_size=16mb,keepalive_time=30s,compression=gzip

# gRPC client options (per-service override)
PROTOBRIDGE_TASK_SERVICE_GRPC_OPTIONS=max_recv_msg_size=64mb
```

The proxy fails fast on startup if any required variable is missing.

### gRPC client options

The `PROTOBRIDGE_GRPC_OPTIONS` variable configures gRPC dial options for all services. Per-service overrides (`PROTOBRIDGE_<SERVICE>_GRPC_OPTIONS`) are applied on top of global options.

Supported keys:

| Key | Type | Example | Description |
|---|---|---|---|
| `max_recv_msg_size` | size | `16mb` | Max inbound message size |
| `max_send_msg_size` | size | `16mb` | Max outbound message size |
| `keepalive_time` | duration | `30s` | Interval between keepalive pings |
| `keepalive_timeout` | duration | `10s` | Timeout for keepalive ping ack |
| `keepalive_permit_without_stream` | bool | `true` | Allow pings without active streams |
| `initial_window_size` | size | `1mb` | Per-stream flow control window |
| `initial_conn_window_size` | size | `2mb` | Per-connection flow control window |
| `compression` | string | `gzip` | Default compressor (`gzip` or `none`) |

Size values accept human-readable suffixes: `kb`, `mb`, `gb`. Values without suffix are bytes. Durations use Go format (`30s`, `5m`, `1h`).

## JSON / oneof marshalling

Proto `oneof` is the standard way to model union types, but `protojson` makes it almost useless for REST APIs: it flattens all variant fields into the parent object with no way to tell which branch is set. Frontend teams end up writing fragile "guess the type" logic or abandoning `oneof` entirely.

protobridge solves this with discriminated unions -- the pattern every frontend framework already expects.

**Proto definition:**

```protobuf
message Task {
  string id = 1;
  oneof attachment {
    FileAttachment file = 2;
    LinkAttachment link = 3;
  }
}
```

**protobridge JSON output (message variants):**

```json
{
  "id": "abc123",
  "attachment": {
    "protobridge_disc": "FileAttachment",
    "filename": "report.pdf",
    "size_bytes": 1024
  }
}
```

The `"protobridge_disc"` field is added automatically. The value is the unqualified proto message name. Frontend can `switch` on it directly. The `protobridge_` prefix guarantees zero collision with user-defined fields -- no one names their fields after third-party libraries.

**Primitive variants** (scalars in a oneof) are inlined without a discriminator -- type inference handles them naturally.

**Safety:**
- Message type names used inside `oneof` blocks must be globally unique across the entire API. Collisions are caught at **generation time**, not runtime.
- Messages used as oneof variants **cannot be used as standalone RPC input types** outside of a oneof. This prevents confusion where the same message sometimes has a discriminator and sometimes doesn't. The generator enforces this at build time.
- The field name `"protobridge_disc"` is **reserved** -- proto messages used inside oneof blocks must not define a field with this name.

**Deserialization** works in reverse: the parser reads the `"protobridge_disc"` field to select the correct proto variant. No ambiguity, no guessing.

## Example

The repo includes a full [taskboard example](example/taskboard/) -- an in-memory CRUD app covering every feature:

- Unary CRUD (create, get, update, delete, list)
- Server streaming (watch task events)
- Client streaming (bulk create)
- Bidirectional streaming (task chat)
- Auth + no-auth endpoints
- Required fields, required headers, query params
- Enums with `x_var_name`
- Oneof (file attachment / link attachment)

```bash
cd example/taskboard

# Generate everything
make all

# Run the gRPC backend
make server

# In another terminal, the generated proxy would be built + run from generated/
```

## Project structure

```
protobridge/
├── proto/protobridge/options.proto     # custom proto extensions
├── cmd/protoc-gen-protobridge/         # protoc plugin
├── internal/
│   ├── generator/                      # code generation (handlers, main, ws, openapi)
│   └── parser/                         # proto descriptor parsing + validation
├── runtime/                            # shared library used by generated code
│   ├── errors.go                       # gRPC → HTTP error mapping
│   ├── sentry.go                       # panic recovery + error reporting
│   ├── metadata.go                     # gRPC metadata helpers
│   ├── auth.go                         # auth middleware
│   ├── json.go                         # custom oneof JSON marshalling
│   ├── query.go                        # query param parsing
│   ├── validate.go                     # required field validation
│   └── ws.go                           # WebSocket proxy
└── example/
    └── taskboard/                      # full working example
```

## Connection scaling

gRPC uses HTTP/2 multiplexing -- multiple requests share a single TCP connection. But each connection has a stream limit (`MAX_CONCURRENT_STREAMS`, default 100). Under high load, a single connection becomes a bottleneck.

protobridge uses adaptive connection scaling via [gox/grpcx](https://github.com/MrS1lentcz/gox):

- **1 to N connections per service**, scaled automatically based on active request count
- **Default threshold**: 100 concurrent streams per connection (matches HTTP/2 default)
- **Default max**: 10 connections per service (handles 1000 concurrent requests)
- **Per-request connection acquisition**: each HTTP request gets the least-loaded connection, released automatically after the response is sent

```
  50 concurrent requests  →  1 gRPC connection
 200 concurrent requests  →  2 gRPC connections
 500 concurrent requests  →  5 gRPC connections
1000 concurrent requests  → 10 gRPC connections (max)
```

No configuration needed -- it works out of the box. The pool also monitors connection health and transparently reconnects on failures.

## Generated output

Running `protoc-gen-protobridge` produces a complete, self-contained directory:

| File | Purpose |
|---|---|
| `main.go` | Entry point: connection pool, Sentry, ENV validation, chi router, graceful shutdown |
| `<service>.go` | HTTP/WS/SSE handlers per gRPC service |
| `schema/openapi.yaml` | OpenAPI 3.1 spec for unary HTTP endpoints |
| `schema/asyncapi.yaml` | AsyncAPI 3.0 spec for WebSocket/streaming endpoints |
| `.env.example` | All ENV variables with comments and placeholders |
| `.env.defaults` | Default values for optional ENV variables |
| `Dockerfile` | Multi-stage build: `golang` → `alpine` |
| `k8s.yaml` | Kubernetes Deployment + Service with health probes and ENV stubs |

The `Dockerfile` and `k8s.yaml` are starting points -- adjust image names, resource limits, and service addresses for your environment.

## Design principles

- **Proto is the source of truth** -- all API shape lives in `.proto` annotations
- **Runtime config via ENV** -- gRPC targets, TLS, ports from environment variables
- **Zero handwritten code** -- the generated binary compiles and runs without user-authored Go
- **Fail fast** -- validation errors surface at generation time or startup, not at runtime
- **Simple constraints over flexibility** -- limited config surface to reduce misconfiguration

## License

MIT
