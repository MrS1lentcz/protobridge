// Minimal gRPC backend for benchmarking. Returns static responses
// with near-zero processing time so the benchmark measures only
// the protobridge proxy overhead.
package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/mrs1lentcz/gox/grpcx/server"
	"google.golang.org/grpc"

	pb "github.com/mrs1lentcz/protobridge/runtime/benchdata"
)

type benchService struct {
	pb.UnimplementedBenchServiceServer
	counter atomic.Int64
}

func (s *benchService) CreateItem(ctx context.Context, req *pb.CreateItemRequest) (*pb.Item, error) {
	id := fmt.Sprintf("item-%d", s.counter.Add(1))
	return &pb.Item{Id: id, Name: req.Name, Priority: req.Priority}, nil
}

func (s *benchService) Authenticate(ctx context.Context, req *pb.AuthRequest) (*pb.AuthResponse, error) {
	return &pb.AuthResponse{UserId: req.Headers["Authorization"], Role: "admin"}, nil
}

func (s *benchService) Subscribe(req *pb.CreateItemRequest, stream pb.BenchService_SubscribeServer) error {
	for i := 0; i < 100; i++ {
		if err := stream.Send(&pb.StreamMessage{Data: fmt.Sprintf("event-%d", i)}); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	log.Println("bench gRPC server starting on :50051")
	if err := server.Run(context.Background(), server.Config{
		Addr:       ":50051",
		Reflection: true,
		RegisterServices: func(s *grpc.Server) error {
			pb.RegisterBenchServiceServer(s, &benchService{})
			return nil
		},
	}); err != nil {
		log.Fatal(err)
	}
}
