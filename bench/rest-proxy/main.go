// Minimal REST proxy for benchmarking. Hand-wired handlers that simulate
// what protoc-gen-protobridge generates, without requiring actual code
// generation.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/mrs1lentcz/gox/grpcx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/benchdata"
)

func main() {
	addr := os.Getenv("GRPC_ADDR")
	if addr == "" {
		addr = "localhost:50051"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	pool := grpcx.NewPool()
	defer pool.Close()

	conn, err := pool.Connect(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("connecting to gRPC: %v", err)
	}

	client := pb.NewBenchServiceClient(conn)

	// Auth function: calls Authenticate RPC.
	authFn := func(ctx context.Context, r *http.Request) ([]byte, error) {
		headers := make(map[string]string)
		for k, v := range r.Header {
			if len(v) > 0 {
				headers[k] = v[0]
			}
		}
		resp, err := client.Authenticate(ctx, &pb.AuthRequest{Headers: headers})
		if err != nil {
			return nil, err
		}
		return proto.Marshal(resp)
	}

	r := chi.NewRouter()
	r.Use(runtime.CORSMiddleware(runtime.CORSConfigFromEnv()))
	r.Use(runtime.SentryMiddleware())
	r.Get("/healthz", runtime.HealthHandler())

	// POST /items – unary with auth
	r.Post("/items", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Auth
		userData, err := authFn(ctx, r)
		if err != nil {
			runtime.WriteAuthError(w, err)
			return
		}
		ctx = runtime.SetUserMetadata(ctx, userData)

		// Decode + validate
		req := &pb.CreateItemRequest{}
		if err := runtime.DecodeRequest(r, req); err != nil {
			runtime.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		if violations := runtime.ValidateRequired(req, []string{"name"}); len(violations) > 0 {
			runtime.WriteValidationError(w, violations)
			return
		}

		// gRPC call
		resp, err := client.CreateItem(ctx, req)
		if err != nil {
			runtime.WriteGRPCError(w, err)
			return
		}

		runtime.WriteResponse(w, http.StatusOK, resp)
	})

	// POST /items/noauth – unary without auth (measures pure proxy overhead)
	r.Post("/items/noauth", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

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
	})

	log.Printf("bench REST proxy on :%s → gRPC %s", port, addr)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatal(err)
	}
}
