package runtime_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/benchdata"
)

// =============================================================================
// In-process gRPC backend
// =============================================================================

type benchServer struct {
	pb.UnimplementedBenchServiceServer
	counter atomic.Int64
}

func (s *benchServer) CreateItem(ctx context.Context, req *pb.CreateItemRequest) (*pb.Item, error) {
	id := fmt.Sprintf("item-%d", s.counter.Add(1))
	return &pb.Item{
		Id:       id,
		Name:     req.Name,
		Priority: req.Priority,
	}, nil
}

func (s *benchServer) Authenticate(ctx context.Context, req *pb.AuthRequest) (*pb.AuthResponse, error) {
	return &pb.AuthResponse{
		UserId: req.Headers["Authorization"],
		Role:   "admin",
	}, nil
}

func (s *benchServer) Subscribe(req *pb.CreateItemRequest, stream pb.BenchService_SubscribeServer) error {
	for i := 0; i < 100; i++ {
		if err := stream.Send(&pb.StreamMessage{Data: fmt.Sprintf("event-%d", i)}); err != nil {
			return err
		}
	}
	return nil
}

// startBenchGRPC starts an in-process gRPC server and returns the client
// connection. The server is stopped when cleanup runs.
func startBenchGRPC(tb testing.TB) *grpc.ClientConn {
	tb.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}

	srv := grpc.NewServer()
	pb.RegisterBenchServiceServer(srv, &benchServer{})

	go srv.Serve(lis)
	tb.Cleanup(func() { srv.Stop() })

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { conn.Close() })

	return conn
}

// =============================================================================
// Handler builders (simulate what the generator produces)
// =============================================================================

func buildUnaryHandler(client pb.BenchServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		req := &pb.CreateItemRequest{}
		if err := runtime.DecodeRequest(r, req); err != nil {
			runtime.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}

		if violations := runtime.ValidateRequired(req, []string{"name"}); len(violations) > 0 {
			runtime.WriteValidationError(w, violations)
			return
		}

		resp, err := client.CreateItem(ctx, req)
		if err != nil {
			runtime.WriteGRPCError(w, err)
			return
		}

		runtime.WriteResponse(w, http.StatusOK, resp)
	}
}

func buildAuthHandler(client pb.BenchServiceClient, authClient pb.BenchServiceClient) http.HandlerFunc {
	authFn := func(ctx context.Context, r *http.Request) ([]byte, error) {
		headers := make(map[string]string)
		for k, v := range r.Header {
			if len(v) > 0 {
				headers[k] = v[0]
			}
		}
		resp, err := authClient.Authenticate(ctx, &pb.AuthRequest{Headers: headers})
		if err != nil {
			return nil, err
		}
		data, err := proto.Marshal(resp)
		if err != nil {
			return nil, err
		}
		return data, nil
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		userData, err := authFn(ctx, r)
		if err != nil {
			runtime.WriteAuthError(w, err)
			return
		}
		ctx = runtime.SetUserMetadata(ctx, userData)

		// Extract user_id header → gRPC metadata
		md := metadata.MD{}
		uid := r.Header.Get("user_id")
		if uid == "" {
			runtime.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "missing user_id")
			return
		}
		md.Set("user_id", uid)
		ctx = metadata.NewOutgoingContext(ctx, md)

		req := &pb.CreateItemRequest{}
		if err := runtime.DecodeRequest(r, req); err != nil {
			runtime.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}

		resp, err := client.CreateItem(ctx, req)
		if err != nil {
			runtime.WriteGRPCError(w, err)
			return
		}

		runtime.WriteResponse(w, http.StatusOK, resp)
	}
}

func buildSSEHandler(client pb.BenchServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		stream, err := client.Subscribe(ctx, &pb.CreateItemRequest{Name: "bench"})
		if err != nil {
			runtime.WriteGRPCError(w, err)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				fmt.Fprintf(w, "event: close\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			if err != nil {
				return
			}
			data, _ := protojson.Marshal(msg)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

// BenchmarkUnaryRequest measures end-to-end throughput for a simple
// unary POST: HTTP request → JSON decode → validation → gRPC call →
// JSON encode → HTTP response.
func BenchmarkUnaryRequest(b *testing.B) {
	conn := startBenchGRPC(b)
	client := pb.NewBenchServiceClient(conn)

	r := chi.NewRouter()
	r.Post("/items", buildUnaryHandler(client))
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := []byte(`{"name":"bench-item","priority":"PRIORITY_HIGH"}`)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			resp, err := http.Post(srv.URL+"/items", "application/json", bytes.NewReader(body))
			if err != nil {
				b.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				b.Fatalf("expected 200, got %d", resp.StatusCode)
			}
		}
	})
}

// BenchmarkUnaryWithAuth measures the same flow with authentication:
// HTTP request → auth gRPC call → proto.Marshal → base64 → metadata →
// gRPC call → response.
func BenchmarkUnaryWithAuth(b *testing.B) {
	conn := startBenchGRPC(b)
	client := pb.NewBenchServiceClient(conn)

	r := chi.NewRouter()
	r.Post("/items", buildAuthHandler(client, client))
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := []byte(`{"name":"bench-item","priority":"PRIORITY_HIGH"}`)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			req, _ := http.NewRequest("POST", srv.URL+"/items", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "user-123")
			req.Header.Set("user_id", "user-123")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				b.Fatalf("expected 200, got %d", resp.StatusCode)
			}
		}
	})
}

// BenchmarkSSEStream measures SSE throughput: one HTTP connection receiving
// 100 server-sent events from a gRPC server stream.
func BenchmarkSSEStream(b *testing.B) {
	conn := startBenchGRPC(b)
	client := pb.NewBenchServiceClient(conn)

	r := chi.NewRouter()
	r.Get("/subscribe", buildSSEHandler(client))
	srv := httptest.NewServer(r)
	defer srv.Close()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, err := http.Get(srv.URL + "/subscribe")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	b.ReportMetric(float64(b.N)*100, "events")
}

// BenchmarkConcurrentConnections measures how many concurrent SSE connections
// the proxy can maintain and the memory cost per connection.
func BenchmarkConcurrentConnections(b *testing.B) {
	conn := startBenchGRPC(b)
	client := pb.NewBenchServiceClient(conn)

	// This handler blocks until client disconnects (simulates long-lived SSE).
	longLivedHandler := func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		stream, err := client.Subscribe(ctx, &pb.CreateItemRequest{Name: "bench"})
		if err != nil {
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		for {
			msg, err := stream.Recv()
			if err != nil {
				return
			}
			data, _ := protojson.Marshal(msg)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}

	r := chi.NewRouter()
	r.Get("/subscribe", longLivedHandler)
	srv := httptest.NewServer(r)
	defer srv.Close()

	// Measure memory for N concurrent connections.
	for _, connCount := range []int{100, 500, 1000} {
		b.Run(fmt.Sprintf("connections_%d", connCount), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// Open connCount concurrent connections.
				contexts := make([]context.CancelFunc, connCount)
				responses := make([]*http.Response, connCount)

				for j := 0; j < connCount; j++ {
					ctx, cancel := context.WithCancel(context.Background())
					contexts[j] = cancel

					req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/subscribe", nil)
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						// Server may reject under load – that's OK for bench.
						cancel()
						continue
					}
					responses[j] = resp
				}

				// Close all connections.
				for j := 0; j < connCount; j++ {
					if contexts[j] != nil {
						contexts[j]()
					}
					if responses[j] != nil {
						responses[j].Body.Close()
					}
				}
			}
		})
	}
}

// BenchmarkHealthEndpoint measures the lightweight /healthz endpoint as
// a baseline. Uses httptest.NewRecorder to avoid TCP socket exhaustion.
func BenchmarkHealthEndpoint(b *testing.B) {
	handler := runtime.HealthHandler()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/healthz", nil)
			handler.ServeHTTP(w, r)
		}
	})
}

// BenchmarkValidation measures the overhead of required field validation
// with protobuf reflection.
func BenchmarkValidation(b *testing.B) {
	msg := &pb.CreateItemRequest{
		Name:     "bench-item",
		Priority: pb.Priority_PRIORITY_HIGH,
	}
	fields := []string{"name", "priority"}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		runtime.ValidateRequired(msg, fields)
	}
}

// BenchmarkJSONMarshal measures proto → JSON marshalling for a typical
// response message.
func BenchmarkJSONMarshal(b *testing.B) {
	msg := &pb.Item{
		Id:       "item-12345",
		Name:     "bench-item",
		Priority: pb.Priority_PRIORITY_HIGH,
	}

	w := httptest.NewRecorder()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		runtime.WriteResponse(w, http.StatusOK, msg)
	}
}

// BenchmarkAuthSerialization measures the auth overhead: proto.Marshal →
// base64 → metadata injection.
func BenchmarkAuthSerialization(b *testing.B) {
	authResp := &pb.AuthResponse{
		UserId: "user-12345",
		Role:   "admin",
	}
	data, _ := proto.Marshal(authResp)

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = base64.StdEncoding.EncodeToString(data)
		runtime.SetUserMetadata(ctx, data)
	}
}
