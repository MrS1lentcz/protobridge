package runtime

import (
	"context"

	"github.com/mrs1lentcz/gox/grpcx"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// UnaryCallWithRetry executes a unary gRPC call. If it fails with a transient
// error, it reconnects through the pool and retries once.
func UnaryCallWithRetry[Req proto.Message, Resp proto.Message](
	ctx context.Context,
	pool *grpcx.Pool,
	addr string,
	call func(ctx context.Context, req Req) (Resp, error),
	req Req,
) (Resp, error) {
	resp, err := call(ctx, req)
	if err != nil && grpcx.IsTransient(err) {
		_, reconnErr := pool.Reconnect(addr)
		if reconnErr != nil {
			return resp, err // return original error
		}
		resp, err = call(ctx, req)
	}
	return resp, err
}

// ReconnectOnTransient checks if an error is transient and reconnects if so.
// Returns the new connection (or the existing one if no reconnect was needed).
// Used by streaming handlers for stateless stream retry.
func ReconnectOnTransient(pool *grpcx.Pool, addr string, err error) (*grpc.ClientConn, bool) {
	if err == nil || !grpcx.IsTransient(err) {
		return nil, false
	}
	conn, reconnErr := pool.Reconnect(addr)
	if reconnErr != nil {
		return nil, false
	}
	return conn, true
}
