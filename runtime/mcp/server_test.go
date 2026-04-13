package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/grpc/metadata"

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

	out, err := srv.CallUnary(context.Background(), req, reqMsg, respMsg, func(ctx context.Context) error {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("expected outgoing metadata")
		}
		capturedMD = md
		// reqMsg should be populated from arguments by now.
		if reqMsg.Name != "alice" || reqMsg.Age != 30 {
			t.Errorf("arguments not unmarshaled: %+v", reqMsg)
		}
		return nil
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
	_, err := srv.CallUnary(context.Background(), req, &pb.SimpleRequest{}, &pb.SimpleResponse{},
		func(_ context.Context) error { t.Fatal("invoke should not run on auth failure"); return nil })
	if err == nil {
		t.Fatal("expected error from auth failure")
	}
}

func TestServer_CallUnary_InvokeError(t *testing.T) {
	srv := mcp.NewServer("t", "0", mcp.DefaultAuthFunc())
	req := mcpsdk.CallToolRequest{}
	want := errors.New("backend down")
	_, err := srv.CallUnary(context.Background(), req, &pb.SimpleRequest{}, &pb.SimpleResponse{},
		func(_ context.Context) error { return want })
	if !errors.Is(err, want) {
		t.Errorf("expected backend error to surface, got %v", err)
	}
}

func TestServer_CallUnary_NoArguments(t *testing.T) {
	// A tool whose request type is google.protobuf.Empty (or whose proto
	// message has no fields) gets an empty MCP arguments map — must not error.
	srv := mcp.NewServer("t", "0", mcp.DefaultAuthFunc())
	req := mcpsdk.CallToolRequest{}
	out, err := srv.CallUnary(context.Background(), req, &pb.SimpleRequest{}, &pb.SimpleResponse{Id: "x"},
		func(_ context.Context) error { return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == nil {
		t.Fatal("expected result")
	}
}
