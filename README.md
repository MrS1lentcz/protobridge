# protobridge

Zero-code gRPC-to-REST proxy generator for Go. Define your API in `.proto` files, run `protoc`, get a fully compilable REST gateway with WebSocket support, OpenAPI spec, authentication, and structured error handling -- no handwritten Go required.

## Why protobridge

Existing gRPC-REST gateways (grpc-gateway, etc.) fall short in areas that frontend teams care about most:

- **Broken `oneof` / union types** -- `protojson` produces flat objects with no discriminator. Frontend can't tell which variant it received. protobridge generates clean discriminated unions with a `"protobridge_disc"` field, validated for global uniqueness at generation time. Oneof variant messages have strict usage rules enforced by the generator, so the API surface is always consistent.
- **Unusable enums** -- proto enums expose raw `SCREAMING_CASE` names and the meaningless `0` default to the API consumer. protobridge strips the zero member entirely and lets you define clean names via `x_var_name` (`"low"`, `"high"` instead of `TASK_PRIORITY_LOW`). The result is an OpenAPI spec that frontend codegen tools can consume directly.
- **No streaming story** -- most gateways either ignore streaming RPCs or require separate configuration. protobridge automatically maps all stream types (server, client, bidi) to WebSocket endpoints. Same proto annotations, same proxy.
- **Boilerplate everywhere** -- even with a gateway, you still write middleware, auth wiring, connection management, error mapping, validation, and a `main.go`. protobridge generates all of it. The output compiles and runs with zero handwritten Go.

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
go install github.com/mrs1lentcz/protobridge/cmd/protoc-gen-protobridge@latest
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
  -I . -I path/to/protobridge/proto -I path/to/googleapis \
  your/service.proto
```

**3. Build and run the generated proxy:**

```bash
cd gateway
go mod init your/gateway && go mod tidy
go build -o gateway .

PROTOBRIDGE_TASK_SERVICE_ADDR=localhost:50051 ./gateway
```

The proxy is now listening on `:8080` and forwarding requests to your gRPC backend.

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

## Environment variables

Generated from proto service names in `SCREAMING_SNAKE_CASE`:

```bash
# gRPC targets (required)
PROTOBRIDGE_TASK_SERVICE_ADDR=task-service:50051
PROTOBRIDGE_AUTH_SERVICE_ADDR=auth-service:50051

# HTTP server
PROTOBRIDGE_PORT=8080                           # default: 8080

# TLS
PROTOBRIDGE_TLS_CERT=/certs/cert.pem            # optional
PROTOBRIDGE_TLS_KEY=/certs/key.pem              # optional
PROTOBRIDGE_TASK_SERVICE_TLS=true               # optional, per service

# Observability
PROTOBRIDGE_SENTRY_DSN=https://...@sentry.io/1  # optional
```

The proxy fails fast on startup if any required variable is missing.

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

## Generated output

Running `protoc-gen-protobridge` produces a complete, self-contained directory:

| File | Purpose |
|---|---|
| `main.go` | Entry point: connection pool, Sentry, ENV validation, chi router, graceful shutdown |
| `<service>.go` | HTTP/WS handlers per gRPC service |
| `openapi.yaml` | OpenAPI 3.1 spec for unary HTTP endpoints |
| `asyncapi.yaml` | AsyncAPI 3.0 spec for WebSocket/streaming endpoints |
| `Dockerfile` | Multi-stage build: `golang:alpine` → `distroless` |
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
