package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/runtime/mcp"
	pb "github.com/mrs1lentcz/protobridge/runtime/testdata"
)

func TestServer_NewServer(t *testing.T) {
	srv := mcp.NewServer("test", "0.0.1", mcp.DefaultAuthFunc("session_id"))
	if srv.Inner() == nil {
		t.Fatal("inner mcp-go server should be initialized")
	}
	if srv.AuthFunc() == nil {
		t.Fatal("AuthFunc should be set")
	}
}

func TestServer_AddTool_Registers(t *testing.T) {
	srv := mcp.NewServer("test", "0.0.1", mcp.DefaultAuthFunc())
	srv.AddTool("ping", "ping the server", json.RawMessage(`{"type":"object"}`),
		func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return mcpsdk.NewToolResultText("pong"), nil
		})
	// Sanity: passing a no-op handler should not panic. The mcp-go server
	// has no public introspection for registered tools, so we trust the
	// AddTool delegation and verify in CallUnary tests below.
	if srv.Inner() == nil {
		t.Fatal("inner nil after AddTool")
	}
}

func TestServer_CallUnary_DispatchAndAuth(t *testing.T) {
	// Verifies the full dispatch loop: arguments → proto message,
	// auth metadata → outgoing context, gRPC invocation, response → text.
	var capturedMD metadata.MD
	auth := mcp.MCPAuthFunc(func(_ context.Context, _ mcp.ConnectionInfo) (metadata.MD, error) {
		return metadata.MD{"session_id": []string{"abc"}}, nil
	})
	srv := mcp.NewServer("t", "0", auth)

	req := mcpsdk.CallToolRequest{}
	req.Params.Name = "create"
	req.Params.Arguments = map[string]any{"name": "alice", "age": 30}

	reqMsg := &pb.SimpleRequest{}
	respMsg := &pb.SimpleResponse{Id: "r1", Name: "alice"}

	out, err := srv.CallUnary(context.Background(), req, reqMsg, func(ctx context.Context, _ proto.Message) (proto.Message, error) {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("expected outgoing metadata")
		}
		capturedMD = md
		// reqMsg should be populated from arguments by now.
		if reqMsg.Name != "alice" || reqMsg.Age != 30 {
			t.Errorf("arguments not unmarshaled: %+v", reqMsg)
		}
		return respMsg, nil
	})
	if err != nil {
		t.Fatalf("CallUnary: %v", err)
	}
	if got := capturedMD.Get("session_id"); len(got) != 1 || got[0] != "abc" {
		t.Errorf("session metadata not propagated: %v", capturedMD)
	}
	// Result is a text-content with marshaled response JSON.
	if len(out.Content) == 0 {
		t.Fatal("expected content")
	}
	if tc, ok := out.Content[0].(mcpsdk.TextContent); !ok || !strings.Contains(tc.Text, `"id":"r1"`) {
		t.Errorf("unexpected content: %#v", out.Content[0])
	}
}

func TestServer_CallUnary_AuthError(t *testing.T) {
	auth := mcp.MCPAuthFunc(func(_ context.Context, _ mcp.ConnectionInfo) (metadata.MD, error) {
		return nil, errors.New("no session")
	})
	srv := mcp.NewServer("t", "0", auth)

	req := mcpsdk.CallToolRequest{}
	_, err := srv.CallUnary(context.Background(), req, &pb.SimpleRequest{},
		func(_ context.Context, _ proto.Message) (proto.Message, error) {
			t.Fatal("invoke should not run on auth failure")
			return nil, nil
		})
	if err == nil {
		t.Fatal("expected error from auth failure")
	}
}

func TestServer_CallUnary_InvokeError(t *testing.T) {
	srv := mcp.NewServer("t", "0", mcp.DefaultAuthFunc())
	req := mcpsdk.CallToolRequest{}
	want := errors.New("backend down")
	_, err := srv.CallUnary(context.Background(), req, &pb.SimpleRequest{},
		func(_ context.Context, _ proto.Message) (proto.Message, error) { return nil, want })
	if !errors.Is(err, want) {
		t.Errorf("expected backend error to surface, got %v", err)
	}
}

func TestServer_ServeFromEnv_UnknownTransport(t *testing.T) {
	t.Setenv("PROTOBRIDGE_MCP_TRANSPORT", "websocket")
	srv := mcp.NewServer("t", "0", mcp.DefaultAuthFunc())
	err := srv.ServeFromEnv(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("expected unknown-transport error, got: %v", err)
	}
}

func TestServer_ServeStdio_RespectsCancelledContext(t *testing.T) {
	srv := mcp.NewServer("t", "0", mcp.DefaultAuthFunc())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled — stdio loop should exit promptly

	done := make(chan error, 1)
	go func() { done <- srv.ServeStdio(ctx) }()
	select {
	case <-done:
		// either nil or context.Canceled is fine — the goal is "exits".
	case <-time.After(2 * time.Second):
		t.Fatal("ServeStdio did not honor cancelled context within 2s")
	}
}

func TestServer_ServeStreamableHTTP_BadAddr(t *testing.T) {
	srv := mcp.NewServer("t", "0", mcp.DefaultAuthFunc())
	// Address not preceded by ':' is invalid per net.Listen — exercises the
	// error return path without occupying a real port.
	if err := srv.ServeStreamableHTTP("not-a-valid-addr"); err == nil {
		t.Error("expected listen error")
	}
}

func TestServer_ServeFromEnv_HTTPRoutesToHTTPServer(t *testing.T) {
	t.Setenv("PROTOBRIDGE_MCP_TRANSPORT", "http")
	t.Setenv("PROTOBRIDGE_MCP_HTTP_ADDR", "not-a-valid-addr")
	srv := mcp.NewServer("t", "0", mcp.DefaultAuthFunc())
	err := srv.ServeFromEnv(context.Background())
	if err == nil {
		t.Error("expected error from invalid HTTP addr")
	}
}

func TestServer_HTTPHeadersFromContext_NilForStdio(t *testing.T) {
	if got := mcp.HTTPHeadersFromContext(context.Background()); got != nil {
		t.Errorf("expected nil for stdio context, got %v", got)
	}
}

func TestServer_CallUnary_NoArguments(t *testing.T) {
	// A tool whose request type is google.protobuf.Empty (or whose proto
	// message has no fields) gets an empty MCP arguments map — must not error.
	srv := mcp.NewServer("t", "0", mcp.DefaultAuthFunc())
	req := mcpsdk.CallToolRequest{}
	resp := &pb.SimpleResponse{Id: "x"}
	out, err := srv.CallUnary(context.Background(), req, &pb.SimpleRequest{},
		func(_ context.Context, _ proto.Message) (proto.Message, error) { return resp, nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == nil {
		t.Fatal("expected result")
	}
}
