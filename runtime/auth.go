package runtime

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// AuthFunc authenticates an HTTP request and returns the serialized user data
// to be forwarded as gRPC metadata.
type AuthFunc func(ctx context.Context, r *http.Request) ([]byte, error)

// AuthCaller is the interface for calling the auth RPC. The generated code
// provides the concrete implementation using the specific proto client.
type AuthCaller interface {
	CallAuth(ctx context.Context, headers map[string]string) (proto.Message, error)
}

// NewAuthFunc creates an AuthFunc that calls the auth RPC via the provided
// connection. The caller parameter is generated code that knows the specific
// proto types.
func NewAuthFunc(conn *grpc.ClientConn) AuthFunc {
	// This is a placeholder – the actual implementation is generated per-API
	// because we need the concrete proto types. The generated code will create
	// an AuthFunc directly.
	_ = conn
	return func(ctx context.Context, r *http.Request) ([]byte, error) {
		return nil, fmt.Errorf("auth not configured")
	}
}

// MakeAuthFunc creates an AuthFunc from an AuthCaller. This is used by the
// generated code which provides the concrete proto client.
func MakeAuthFunc(caller AuthCaller) AuthFunc {
	return func(ctx context.Context, r *http.Request) ([]byte, error) {
		headers := make(map[string]string)
		for k, v := range r.Header {
			if len(v) > 0 {
				headers[k] = v[0]
			}
		}

		resp, err := caller.CallAuth(ctx, headers)
		if err != nil {
			return nil, err
		}

		data, err := proto.Marshal(resp)
		if err != nil {
			return nil, fmt.Errorf("marshalling auth response: %w", err)
		}

		return data, nil
	}
}

// NoAuth returns an AuthFunc that always succeeds with nil user data.
// Used when no auth_method is defined.
func NoAuth() AuthFunc {
	return func(ctx context.Context, r *http.Request) ([]byte, error) {
		return nil, nil
	}
}
