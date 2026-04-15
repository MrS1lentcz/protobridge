// Package eventsgen is the codegen backend for the protoc-gen-events-go
// plugin. It emits typed Emit*/Subscribe* helpers (one .go file per Go
// package) plus an AsyncAPI 3.0 schema describing every (protobridge.event)
// annotated message in the request.
package eventsgen

import (
	"fmt"
	"sort"
	"text/template"

	"github.com/mrs1lentcz/protobridge/internal/generator"
	"github.com/mrs1lentcz/protobridge/internal/parser"
)

// generateServiceBroadcastFile renders the broadcast wireup for one
// (protobridge.broadcast) service. Each output file contains:
//
//   - <Svc>Route, <Svc>Subjects — wire-format constants exported for callers
//     that mount the handler manually or compose with other broadcast groups.
//   - <Svc>Envelope — per-subject proto → protojson marshaler producing the
//     runtime JSON envelope shape (subject + labels + event).
//   - <Svc>Source — events.BroadcastSource implementation that opens the
//     server-streaming gRPC RPC and emits BroadcastFrame to the hub.
//   - New<Svc>Server(bus) — backend-side bus → stream adapter implementing
//     pb.<Svc>Server. Subscribes to every Subject and packs each event into
//     the typed envelope (oneof + labels) before sending.
//   - Register<Svc>Broadcast — one-call wireup mounting NewBroadcastHub at
//     the service's declared route.
//
// When the envelope references event messages from multiple Go packages the
// imports are aliased pb0, pb1, … (deterministic, collision-free). The
// service's own package is always imported under alias `svcpb` so generated
// references to gRPC stub types (Client/Server) stay readable.
func generateServiceBroadcastFile(svc *parser.BroadcastService, outputPkg string) string {
	// Collect unique event GoPackages in declaration order.
	seen := map[string]int{}
	var pkgs []string
	for _, ev := range svc.Events {
		pkg := ev.GoPackage
		if pkg == "" {
			pkg = svc.GoPackage
		}
		if _, ok := seen[pkg]; !ok {
			seen[pkg] = len(pkgs)
			pkgs = append(pkgs, pkg)
		}
	}
	imports := make([]broadcastImport, len(pkgs))
	for i, p := range pkgs {
		alias := "pb"
		if len(pkgs) > 1 {
			alias = fmt.Sprintf("pb%d", i)
		}
		imports[i] = broadcastImport{Alias: alias, Path: p}
	}
	// svcAlias is the import alias under which the streaming service's gRPC
	// stubs live. If the service's own package is already in `imports` (any
	// event lives in it), reuse that alias — adding a second import for the
	// same path would not compile. Otherwise add a fresh import.
	svcAlias := "svcpb"
	if idx, ok := seen[svc.GoPackage]; ok {
		svcAlias = imports[idx].Alias
	} else {
		imports = append(imports, broadcastImport{Alias: svcAlias, Path: svc.GoPackage})
	}

	tmplEvents := make([]tmplBroadcastServiceEvent, 0, len(svc.Events))
	for _, ev := range svc.Events {
		pkg := ev.GoPackage
		if pkg == "" {
			pkg = svc.GoPackage
		}
		tmplEvents = append(tmplEvents, tmplBroadcastServiceEvent{
			MessageName:    ev.Message.Name,
			Subject:        ev.Subject,
			Alias:          imports[seen[pkg]].Alias,
			OneofFieldName: goCamelCase(ev.OneofFieldName),
		})
	}

	data := broadcastServiceFileData{
		PkgName:      outputPkg,
		ServiceName:  svc.Name,
		MethodName:   svc.MethodName,
		Route:        svc.Route,
		EnvelopeName: svc.Envelope.Name,
		HasLabels:    svc.LabelsField != nil,
		Imports:      imports,
		ServiceAlias: svcAlias,
		Events:       tmplEvents,
	}
	return generator.RenderTemplate(broadcastServiceTmpl, data)
}

// goCamelCase mirrors protoc-gen-go's camel-casing for proto identifiers used
// as Go names — verbatim port of google.golang.org/protobuf/internal/strs
// .GoCamelCase (which is internal and can't be imported). Used to derive
// oneof wrapper struct names (e.g. `Envelope_<FieldName>`) and accessors so
// generated code matches what protoc-gen-go emits next to it.
//
// Notable rules: leading underscore becomes 'X' (Go can't start with _),
// internal `_<lower>` drops the underscore and capitalises, dots become '_',
// digits stay where they are. Initialisms like HTTP/URL are NOT normalised —
// protoc-gen-go doesn't normalise them either, so the output stays in lock-step.
func goCamelCase(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '.' && i+1 < len(s) && isASCIILower(s[i+1]):
			// Skip '.' when followed by lowercase — historic behaviour.
		case c == '.':
			b = append(b, '_')
		case c == '_' && (i == 0 || s[i-1] == '.'):
			b = append(b, 'X')
		case c == '_' && i+1 < len(s) && isASCIILower(s[i+1]):
			// Drop '_' before a lowercase letter; the loop's default branch
			// will uppercase that letter on the next iteration.
		case isASCIIDigit(c):
			b = append(b, c)
		default:
			if isASCIILower(c) {
				c -= 'a' - 'A'
			}
			b = append(b, c)
			for ; i+1 < len(s) && isASCIILower(s[i+1]); i++ {
				b = append(b, s[i+1])
			}
		}
	}
	return string(b)
}

func isASCIILower(c byte) bool { return 'a' <= c && c <= 'z' }
func isASCIIDigit(c byte) bool { return '0' <= c && c <= '9' }

type broadcastImport struct {
	Alias string
	Path  string
}

type broadcastServiceFileData struct {
	PkgName      string
	ServiceName  string
	MethodName   string
	Route        string
	EnvelopeName string
	HasLabels    bool
	Imports      []broadcastImport
	ServiceAlias string
	Events       []tmplBroadcastServiceEvent
}

type tmplBroadcastServiceEvent struct {
	MessageName    string
	Subject        string
	Alias          string // which imported package the message lives in
	OneofFieldName string // PascalCase of the oneof field — Go struct member + wrapper-type suffix
}

var broadcastServiceTmpl = template.Must(template.New("broadcast_service").Parse(`// Code generated by protoc-gen-events-go. DO NOT EDIT.

package {{ .PkgName }}

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/mrs1lentcz/protobridge/runtime/events"
{{ range .Imports }}	{{ .Alias }} {{ printf "%q" .Path }}
{{ end }})

// {{ .ServiceName }}Route is the HTTP path declared on the
// (protobridge.broadcast) service. Exported so custom routers can mount the
// hub without duplicating the literal.
const {{ .ServiceName }}Route = {{ printf "%q" .Route }}

// {{ .ServiceName }}Subjects lists every subject that appears in the envelope
// oneof, in proto declaration order.
var {{ .ServiceName }}Subjects = []string{
{{- range .Events }}
	{{ printf "%q" .Subject }},
{{- end }}
}

// {{ .ServiceName }}Envelope decodes a frame's payload for any of the subjects
// above and returns the JSON envelope emitted to WS clients.
func {{ .ServiceName }}Envelope(subject string, payload []byte, labels map[string]string) ([]byte, error) {
	jsonOpts := protojson.MarshalOptions{UseProtoNames: true}
	var msg proto.Message
	switch subject {
{{- range .Events }}
	case {{ printf "%q" .Subject }}:
		msg = &{{ .Alias }}.{{ .MessageName }}{}
{{- end }}
	default:
		return nil, fmt.Errorf("unknown broadcast subject %q", subject)
	}
	if err := proto.Unmarshal(payload, msg); err != nil {
		return nil, fmt.Errorf("decode %s: %w", subject, err)
	}
	encoded, err := jsonOpts.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", subject, err)
	}
	return events.MarshalJSONEnvelope(subject, json.RawMessage(encoded), labels)
}

// {{ .ServiceName }}Source opens the server-streaming gRPC RPC against the
// backend and emits one BroadcastFrame per envelope received. It implements
// events.BroadcastSource and is intended to back a single BroadcastHub per
// gateway pod.
type {{ .ServiceName }}Source struct {
	conn grpc.ClientConnInterface
}

// New{{ .ServiceName }}Source wraps an existing gRPC client connection. The
// connection is dialed by the caller (typically the generated main.go) so
// auth/TLS/keepalive policy stays under user control.
func New{{ .ServiceName }}Source(conn grpc.ClientConnInterface) *{{ .ServiceName }}Source {
	return &{{ .ServiceName }}Source{conn: conn}
}

// Run opens the stream and forwards every received envelope into out as a
// BroadcastFrame. Returns when ctx is cancelled, the stream ends, or any
// non-recoverable transport error occurs. The hub logs and stops on return —
// callers wanting reconnect should wrap this in a retry loop.
func (s *{{ .ServiceName }}Source) Run(ctx context.Context, out chan<- events.BroadcastFrame) error {
	client := {{ .ServiceAlias }}.New{{ .ServiceName }}Client(s.conn)
	stream, err := client.{{ .MethodName }}(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("{{ .ServiceName }}Source: open stream: %w", err)
	}
	for {
		env, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("{{ .ServiceName }}Source: recv: %w", err)
		}
		frame, ok := frameFrom{{ .EnvelopeName }}(env)
		if !ok {
			continue // unknown oneof variant — drop, don't kill the stream
		}
		select {
		case out <- frame:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// frameFrom{{ .EnvelopeName }} unpacks the typed envelope into the runtime's
// transport-agnostic BroadcastFrame. Returns ok=false for envelopes whose
// oneof wasn't set (defensive — backend should never send these).
func frameFrom{{ .EnvelopeName }}(env *{{ .ServiceAlias }}.{{ .EnvelopeName }}) (events.BroadcastFrame, bool) {
	if env == nil {
		return events.BroadcastFrame{}, false
	}
	{{ if .HasLabels -}}
	labels := env.GetLabels()
	{{ else -}}
	var labels map[string]string
	{{ end -}}
	switch v := env.GetEvent().(type) {
{{- range .Events }}
	case *{{ $.ServiceAlias }}.{{ $.EnvelopeName }}_{{ .OneofFieldName }}:
		payload, err := proto.Marshal(v.{{ .OneofFieldName }})
		if err != nil {
			return events.BroadcastFrame{}, false
		}
		return events.BroadcastFrame{Subject: {{ printf "%q" .Subject }}, Payload: payload, Labels: labels}, true
{{- end }}
	}
	return events.BroadcastFrame{}, false
}

// {{ .ServiceName }}Server is the backend-side bus → stream adapter. Embed it
// (or assign it directly) when registering the gRPC server, and the streaming
// RPC will be served by subscribing to every subject in {{ .ServiceName }}Subjects
// on the supplied bus and forwarding each event into the gRPC stream as a
// typed envelope.
type {{ .ServiceName }}Server struct {
	{{ .ServiceAlias }}.Unimplemented{{ .ServiceName }}Server
	bus events.Bus
}

// New{{ .ServiceName }}Server returns the backend-side adapter. Register it
// with your gRPC server: pb.Register{{ .ServiceName }}Server(srv, eventspkg.New{{ .ServiceName }}Server(bus)).
func New{{ .ServiceName }}Server(bus events.Bus) *{{ .ServiceName }}Server {
	return &{{ .ServiceName }}Server{bus: bus}
}

// {{ .MethodName }} implements the streaming RPC. One subscription per subject is
// registered on the bus for the lifetime of the call; on each delivered
// message the typed envelope is built and sent. The call exits when the
// stream context is cancelled (client/gateway disconnect).
func (s *{{ .ServiceName }}Server) {{ .MethodName }}(_ *emptypb.Empty, stream grpc.ServerStreamingServer[{{ .ServiceAlias }}.{{ .EnvelopeName }}]) error {
	ctx := stream.Context()
	send := func(env *{{ .ServiceAlias }}.{{ .EnvelopeName }}) error {
		return stream.Send(env)
	}
	var subs []events.Subscription
	defer func() {
		for _, sub := range subs {
			_ = sub.Unsubscribe()
		}
	}()
{{- range .Events }}
	{
		sub, err := s.bus.SubscribeBroadcast({{ printf "%q" .Subject }}, func(_ context.Context, m events.Message) error {
			ev := &{{ .Alias }}.{{ .MessageName }}{}
			if err := proto.Unmarshal(m.Payload, ev); err != nil {
				return fmt.Errorf("{{ $.ServiceName }}Server: decode %s: %w", {{ printf "%q" .Subject }}, err)
			}
			env := &{{ $.ServiceAlias }}.{{ $.EnvelopeName }}{
				{{ if $.HasLabels -}}
				Labels: events.HeadersToLabels(m.Headers),
				{{ end -}}
				Event: &{{ $.ServiceAlias }}.{{ $.EnvelopeName }}_{{ .OneofFieldName }}{ {{ .OneofFieldName }}: ev },
			}
			return send(env)
		})
		if err != nil {
			return fmt.Errorf("{{ $.ServiceName }}Server: subscribe %s: %w", {{ printf "%q" .Subject }}, err)
		}
		subs = append(subs, sub)
	}
{{- end }}
	<-ctx.Done()
	return nil
}

// Register{{ .ServiceName }}Broadcast mounts the WebSocket broadcast endpoint at
// "<prefix>" using a long-lived gRPC stream against conn as the event source.
// The hub runs until ctx is cancelled — pass a context that lives as long as
// the gateway process.
//
// extra is merged into the BroadcastConfig — pass it to wire auth-derived
// principal labels (PrincipalLabels), a custom Matcher, OriginPatterns, a
// per-client buffer, or a Logger. Source and Marshal are filled in
// automatically and take precedence over equivalents in extra.
func Register{{ .ServiceName }}Broadcast(ctx context.Context, r chi.Router, conn grpc.ClientConnInterface, prefix string, extra ...events.BroadcastConfig) {
	cfg := events.BroadcastConfig{}
	if len(extra) > 0 {
		cfg = extra[0]
	}
	cfg.Source = New{{ .ServiceName }}Source(conn)
	cfg.Marshal = {{ .ServiceName }}Envelope
	hub := events.NewBroadcastHub(ctx, cfg)
	r.Method(http.MethodGet, prefix, hub)
}
`))

// generateEventsFile renders one Go file per Go package containing every
// event from that package. Grouping by package keeps the output flat and
// matches the convention `protoc-gen-go` uses (one .pb.go per .proto file)
// — but events are message-level annotations that may live anywhere in the
// proto graph, so we group by the resolved Go import path instead.
func generateEventsFile(pkgPath, outputPkg string, events []*parser.Event) string {
	if len(events) == 0 {
		// Caller (Generate) only ever passes a non-empty slice — defensive
		// panic surfaces a regression instead of silently emitting nothing.
		panic("eventsgen: no events for package " + pkgPath)
	}

	// Stable order: alphabetical by message name. Generated files diff only
	// when the proto changes.
	sort.Slice(events, func(i, j int) bool {
		return events[i].Message.Name < events[j].Message.Name
	})

	data := fileData{
		ProtoImport: pkgPath,
		PkgName:     outputPkg,
	}
	for _, ev := range events {
		td := tmplEvent{
			MessageName:    ev.Message.Name,
			Subject:        ev.Subject,
			DurableGroup:   ev.DurableGroup,
			Description:    ev.Description,
			KindBroadcast:  ev.Kind == parser.EventKindBroadcast,
			KindDurable:    ev.Kind == parser.EventKindDurable,
			KindBoth:       ev.Kind == parser.EventKindBoth,
			AckWaitSeconds: ev.AckWaitSeconds,
			MaxDeliver:     ev.MaxDeliver,
		}
		data.Events = append(data.Events, td)
	}

	return generator.RenderTemplate(eventsTmpl, data)
}

type fileData struct {
	ProtoImport string
	PkgName     string
	Events      []tmplEvent
}

type tmplEvent struct {
	MessageName    string
	Subject        string
	DurableGroup   string
	Description    string
	KindBroadcast  bool
	KindDurable    bool
	KindBoth       bool
	AckWaitSeconds uint32
	MaxDeliver     uint32
}

var eventsTmpl = template.Must(template.New("events").Parse(`// Code generated by protoc-gen-events-go. DO NOT EDIT.

package {{ .PkgName }}

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/runtime/events"
	pb "{{ .ProtoImport }}"
)

// Suppress unused-import warnings when no durable events are present
// (time is only touched by the heartbeat wrapper generated for durable
// subscribers). The var consumes one reference so the import always
// compiles even if every event is BROADCAST-only.
var _ = time.Second

{{ range .Events }}

// Subject{{ .MessageName }} is the bus subject this event is published on.
const Subject{{ .MessageName }} = {{ printf "%q" .Subject }}

// Emit{{ .MessageName }} marshals ev and publishes it on the bus using the
// kind declared in the proto annotation. Labels stashed on ctx via
// events.WithLabels are forwarded as message headers so subscribers
// (and the broadcast WS endpoint) can route on them.{{ if .Description }}
//
// {{ .Description }}{{ end }}
func Emit{{ .MessageName }}(ctx context.Context, bus events.Bus, ev *pb.{{ .MessageName }}) error {
	if ev == nil {
		return fmt.Errorf("Emit{{ .MessageName }}: nil event")
	}
	payload, err := proto.Marshal(ev)
	if err != nil {
		return fmt.Errorf("Emit{{ .MessageName }}: marshal: %w", err)
	}
	headers := events.LabelsToHeaders(events.LabelsFromContext(ctx), nil)
	return bus.Publish(ctx, Subject{{ .MessageName }}, payload, {{ if .KindBroadcast }}events.KindBroadcast{{ else if .KindDurable }}events.KindDurable{{ else if .KindBoth }}events.KindBoth{{ else }}events.KindUnspecified{{ end }}, headers)
}

// {{ .MessageName }}Handler is the typed signature for {{ .MessageName }}
// subscribers — declared once and reused by both Subscribe{{ .MessageName }}
// (durable) and SubscribeBroadcast{{ .MessageName }} (fan-out) so callers
// can swap between transports without changing the handler type.
type {{ .MessageName }}Handler func(ctx context.Context, ev *pb.{{ .MessageName }}) error

{{ if or .KindDurable .KindBoth }}
// Subscribe{{ .MessageName }} registers a load-balanced at-least-once
// subscriber. group identifies the consumer group; multiple processes in
// the same group split the message stream. Pass an empty string to use
// {{ if .DurableGroup }}{{ printf "%q" .DurableGroup }} (declared in the proto annotation){{ else }}the message name as the default group identifier{{ end }}.
//
// The generated wrapper spawns a heartbeat goroutine that calls
// m.InProgress on an {{ if .AckWaitSeconds }}{{ .AckWaitSeconds }}s{{ else }}AckWait-derived{{ end }} timer so long-running handlers never
// race the ack deadline — a delivery can only be redelivered by the
// backend when the process dies, the handler panics (recovered → Nack),
// or the handler returns an error. Deadlocked handlers inside a live
// process are the one case the runtime can't detect; application-level
// timeouts / watchdogs remain the caller's responsibility.
func Subscribe{{ .MessageName }}(bus events.Bus, group string, h {{ .MessageName }}Handler, opts ...events.DurableOption) (events.Subscription, error) {
	if group == "" {
		group = {{ if .DurableGroup }}{{ printf "%q" .DurableGroup }}{{ else }}Subject{{ .MessageName }}{{ end }}
	}
	annotationOpts := []events.DurableOption{
{{ if .AckWaitSeconds }}		events.WithAckWait({{ .AckWaitSeconds }} * time.Second),
{{ end }}{{ if .MaxDeliver }}		events.WithMaxDeliver({{ .MaxDeliver }}),
{{ end }}	}
	// Caller opts win — they're appended after annotation defaults so
	// later calls overwrite earlier ones in DurableConfig.
	resolved := append(annotationOpts, opts...)
	cfg := events.ResolveDurableConfig(Subject{{ .MessageName }}, resolved...)
	return bus.SubscribeDurable(Subject{{ .MessageName }}, group, func(ctx context.Context, m events.Message) (err error) {
		// Recover panics into a Nack so the subscriber goroutine survives
		// and JetStream redelivers after AckWait. Without this a single
		// handler panic would kill the consume goroutine silently.
		defer func() {
			if rec := recover(); rec != nil {
				m.Nack()
				err = fmt.Errorf("Subscribe{{ .MessageName }}: handler panic: %v", rec)
			}
		}()

		// Heartbeat ticker: fire at half the configured AckWait so a
		// single missed tick still leaves the other one inside the
		// deadline. Stops when the handler returns, so a returned
		// handler never issues a stale InProgress.
		heartbeatStop := make(chan struct{})
		heartbeatDone := make(chan struct{})
		go func() {
			defer close(heartbeatDone)
			ticker := time.NewTicker(cfg.AckWait / 2)
			defer ticker.Stop()
			for {
				select {
				case <-heartbeatStop:
					return
				case <-ticker.C:
					_ = m.InProgress()
				}
			}
		}()
		defer func() {
			close(heartbeatStop)
			<-heartbeatDone
		}()

		ev := &pb.{{ .MessageName }}{}
		if err := proto.Unmarshal(m.Payload, ev); err != nil {
			m.Nack()
			return fmt.Errorf("Subscribe{{ .MessageName }}: unmarshal: %w", err)
		}
		if err := h(ctx, ev); err != nil {
			m.Nack()
			return err
		}
		m.Ack()
		return nil
	}, resolved...)
}
{{ end }}

{{ if or .KindBroadcast .KindBoth }}
// SubscribeBroadcast{{ .MessageName }} registers an ephemeral fan-out
// subscriber. Every subscriber sees every message; missed messages while
// disconnected are not redelivered.
func SubscribeBroadcast{{ .MessageName }}(bus events.Bus, h {{ .MessageName }}Handler) (events.Subscription, error) {
	return bus.SubscribeBroadcast(Subject{{ .MessageName }}, func(ctx context.Context, m events.Message) error {
		ev := &pb.{{ .MessageName }}{}
		if err := proto.Unmarshal(m.Payload, ev); err != nil {
			return fmt.Errorf("SubscribeBroadcast{{ .MessageName }}: unmarshal: %w", err)
		}
		return h(ctx, ev)
	})
}
{{ end }}

{{ end }}
`))
