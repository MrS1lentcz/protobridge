// Minimal REST proxy for benchmarking. Hand-wired handlers that simulate
// what protoc-gen-protobridge generates, using ConnectScaled for adaptive
// connection scaling.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

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
	pool.EnableHealthWatch(30 * time.Second)
	defer pool.Close()

	scalingCfg := grpcx.ScalingConfig{
		StreamsPerConn: 100,
		MaxConns:       10,
	}

	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	// Pre-create first connection (fail-fast).
	if _, err := pool.ConnectScaled(addr, scalingCfg, dialOpts...); err != nil {
		log.Fatalf("connecting to gRPC: %v", err)
	}

	r := chi.NewRouter()
	r.Use(runtime.CORSMiddleware(runtime.CORSConfigFromEnv()))
	r.Use(runtime.SentryMiddleware())
	r.Get("/healthz", runtime.HealthHandler())

	// POST /items – unary with auth
	r.Post("/items", func(w http.ResponseWriter, r *http.Request) {
		conn, err := pool.ConnectScaled(addr, scalingCfg, dialOpts...)
		if err != nil {
			runtime.WriteError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "pool exhausted")
			return
		}
		defer pool.Release(addr, conn)

		client := pb.NewBenchServiceClient(conn)
		ctx := r.Context()

		// Auth
		headers := make(map[string]string)
		for k, v := range r.Header {
			if len(v) > 0 {
				headers[k] = v[0]
			}
		}
		authResp, err := client.Authenticate(ctx, &pb.AuthRequest{Headers: headers})
		if err != nil {
			runtime.WriteAuthError(w, err)
			return
		}
		userData, _ := proto.Marshal(authResp)
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

		resp, err := client.CreateItem(ctx, req)
		if err != nil {
			runtime.WriteGRPCError(w, err)
			return
		}
		runtime.WriteResponse(w, http.StatusOK, resp)
	})

	// POST /items/noauth – unary without auth
	r.Post("/items/noauth", func(w http.ResponseWriter, r *http.Request) {
		conn, err := pool.ConnectScaled(addr, scalingCfg, dialOpts...)
		if err != nil {
			runtime.WriteError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "pool exhausted")
			return
		}
		defer pool.Release(addr, conn)

		client := pb.NewBenchServiceClient(conn)

		req := &pb.CreateItemRequest{}
		if err := runtime.DecodeRequest(r, req); err != nil {
			runtime.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}

		resp, err := client.CreateItem(r.Context(), req)
		if err != nil {
			runtime.WriteGRPCError(w, err)
			return
		}
		runtime.WriteResponse(w, http.StatusOK, resp)
	})

	log.Printf("bench REST proxy on :%s → gRPC %s (scaled: %d streams/conn, max %d conns)",
		port, addr, scalingCfg.StreamsPerConn, scalingCfg.MaxConns)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatal(err)
	}
}
