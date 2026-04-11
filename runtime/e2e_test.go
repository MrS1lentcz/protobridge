package runtime_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/benchdata"
)

// startE2EGRPC starts a fresh gRPC server for E2E tests and returns the client conn.
func startE2EGRPC(t *testing.T) *grpc.ClientConn {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := grpc.NewServer()
	pb.RegisterBenchServiceServer(srv, &benchServer{})

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// buildE2ERouter constructs a chi router wired to real gRPC handlers.
func buildE2ERouter(t *testing.T, conn *grpc.ClientConn) chi.Router {
	t.Helper()

	client := pb.NewBenchServiceClient(conn)

	r := chi.NewRouter()

	// /healthz
	r.Get("/healthz", runtime.HealthHandler())

	// POST /items -- unary with validation
	r.Post("/items", buildUnaryHandler(client))

	// POST /items/auth -- unary with auth + required header
	r.Post("/items/auth", buildAuthHandler(client, client))

	// GET /subscribe -- SSE
	r.Get("/subscribe", buildSSEHandler(client))

	// GET /ws/subscribe -- WebSocket server stream
	r.Get("/ws/subscribe", buildWSServerStreamHandler(client))

	// GET /ws/chat -- WebSocket bidi stream
	r.Get("/ws/chat", buildWSBidiHandler(client))

	return r
}

func buildWSServerStreamHandler(client pb.BenchServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()

		ctx := r.Context()
		stream, err := client.Subscribe(ctx, &pb.CreateItemRequest{Name: "ws-test"})
		if err != nil {
			_ = ws.Close(websocket.StatusInternalError, "stream open failed")
			return
		}

		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				_ = ws.Close(websocket.StatusNormalClosure, "stream ended")
				return
			}
			if err != nil {
				_ = ws.Close(websocket.StatusInternalError, "stream error")
				return
			}
			data, _ := protojson.Marshal(msg)
			if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		}
	}
}

func buildWSBidiHandler(client pb.BenchServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()

		ctx := r.Context()
		stream, err := client.Chat(ctx)
		if err != nil {
			_ = ws.Close(websocket.StatusInternalError, "stream open failed")
			return
		}

		// gRPC → WS
		go func() {
			defer func() { _ = recover() }()
			for {
				msg, err := stream.Recv()
				if err != nil {
					_ = ws.Close(websocket.StatusNormalClosure, "done")
					return
				}
				data, _ := protojson.Marshal(msg)
				if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
					return
				}
			}
		}()

		// WS → gRPC
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				_ = stream.CloseSend()
				return
			}
			msg := &pb.StreamMessage{}
			if err := protojson.Unmarshal(data, msg); err != nil {
				_ = ws.Close(websocket.StatusInvalidFramePayloadData, "bad json")
				return
			}
			if err := stream.Send(msg); err != nil {
				return
			}
		}
	}
}

func TestE2E_HealthEndpoint(t *testing.T) {
	conn := startE2EGRPC(t)
	router := buildE2ERouter(t, conn)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", result["status"])
	}
}

func TestE2E_CreateItem_ValidRequest(t *testing.T) {
	conn := startE2EGRPC(t)
	router := buildE2ERouter(t, conn)
	srv := httptest.NewServer(router)
	defer srv.Close()

	body := `{"name":"test-item","priority":"PRIORITY_HIGH"}`
	resp, err := http.Post(srv.URL+"/items", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /items error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, respBody)
	}

	var item map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if _, ok := item["id"]; !ok {
		t.Fatal("response missing 'id' field")
	}
	if item["name"] != "test-item" {
		t.Fatalf("expected name 'test-item', got %v", item["name"])
	}
	if item["priority"] != "PRIORITY_HIGH" {
		t.Fatalf("expected priority 'PRIORITY_HIGH', got %v", item["priority"])
	}
}

func TestE2E_CreateItem_EmptyBody(t *testing.T) {
	conn := startE2EGRPC(t)
	router := buildE2ERouter(t, conn)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/items", "application/json", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST /items error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Empty body with required "name" field should fail validation (422).
	if resp.StatusCode != http.StatusUnprocessableEntity {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 422, got %d: %s", resp.StatusCode, respBody)
	}

	var apiErr map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if apiErr["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR code, got %v", apiErr["code"])
	}
}

func TestE2E_CreateItemAuth_WithHeaders(t *testing.T) {
	conn := startE2EGRPC(t)
	router := buildE2ERouter(t, conn)
	srv := httptest.NewServer(router)
	defer srv.Close()

	body := `{"name":"auth-item","priority":"PRIORITY_LOW"}`
	req, _ := http.NewRequest("POST", srv.URL+"/items/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "user-42")
	req.Header.Set("user_id", "user-42")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /items/auth error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, respBody)
	}

	var item map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if item["name"] != "auth-item" {
		t.Fatalf("expected name 'auth-item', got %v", item["name"])
	}
}

func TestE2E_CreateItemAuth_MissingRequiredHeader(t *testing.T) {
	conn := startE2EGRPC(t)
	router := buildE2ERouter(t, conn)
	srv := httptest.NewServer(router)
	defer srv.Close()

	body := `{"name":"auth-item"}`
	req, _ := http.NewRequest("POST", srv.URL+"/items/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "user-42")
	// Deliberately omit user_id header.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /items/auth error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, respBody)
	}
}

func TestE2E_SSE_ReceivesEvents(t *testing.T) {
	conn := startE2EGRPC(t)
	router := buildE2ERouter(t, conn)
	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/subscribe", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /subscribe error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream, got %q", ct)
	}

	// Read SSE events. The bench server sends 100 events then closes.
	scanner := bufio.NewScanner(resp.Body)
	eventCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			// Verify it's valid JSON.
			var msg pb.StreamMessage
			if err := protojson.Unmarshal([]byte(data), &msg); err != nil {
				t.Fatalf("invalid SSE data JSON: %v", err)
			}
			if msg.Data == "" {
				t.Fatal("expected non-empty data field in stream message")
			}
			eventCount++
		}
		if strings.HasPrefix(line, "event: close") {
			break
		}
	}

	if eventCount < 1 {
		t.Fatal("expected at least 1 SSE event")
	}
	if eventCount != 100 {
		t.Fatalf("expected 100 SSE events from bench server, got %d", eventCount)
	}
}

func TestE2E_MultipleRequestsSequential(t *testing.T) {
	// Verify the proxy handles multiple sequential requests correctly.
	conn := startE2EGRPC(t)
	router := buildE2ERouter(t, conn)
	srv := httptest.NewServer(router)
	defer srv.Close()

	for i := 0; i < 5; i++ {
		body := fmt.Sprintf(`{"name":"item-%d","priority":"PRIORITY_LOW"}`, i)
		resp, err := http.Post(srv.URL+"/items", "application/json", bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatalf("request %d error: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: expected 200, got %d", i, resp.StatusCode)
		}
	}
}

func TestE2E_WS_ServerStream(t *testing.T) {
	conn := startE2EGRPC(t)
	router := buildE2ERouter(t, conn)
	srv := httptest.NewServer(router)
	defer srv.Close()

	// Connect via WebSocket.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/subscribe"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("WS dial error: %v", err)
	}
	defer func() { _ = ws.CloseNow() }()

	// Receive 100 messages from server stream.
	received := 0
	for i := 0; i < 100; i++ {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("WS read error at message %d: %v", i, err)
		}

		var msg pb.StreamMessage
		if err := protojson.Unmarshal(data, &msg); err != nil {
			t.Fatalf("invalid JSON at message %d: %v", i, err)
		}
		if msg.Data == "" {
			t.Fatalf("empty data at message %d", i)
		}
		received++
	}

	if received != 100 {
		t.Fatalf("expected 100 WS messages, got %d", received)
	}
}

func TestE2E_WS_Bidi(t *testing.T) {
	conn := startE2EGRPC(t)
	router := buildE2ERouter(t, conn)
	srv := httptest.NewServer(router)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/chat"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("WS dial error: %v", err)
	}
	defer func() { _ = ws.CloseNow() }()

	// Send 5 messages and verify echo responses.
	for i := 0; i < 5; i++ {
		msg := &pb.StreamMessage{Data: fmt.Sprintf("hello-%d", i)}
		data, _ := protojson.Marshal(msg)

		if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatalf("WS write error at %d: %v", i, err)
		}

		_, respData, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("WS read error at %d: %v", i, err)
		}

		var resp pb.StreamMessage
		if err := protojson.Unmarshal(respData, &resp); err != nil {
			t.Fatalf("invalid echo JSON at %d: %v", i, err)
		}

		expected := fmt.Sprintf("echo:hello-%d", i)
		if resp.Data != expected {
			t.Fatalf("expected %q, got %q", expected, resp.Data)
		}
	}

	// Clean close.
	_ = ws.Close(websocket.StatusNormalClosure, "done")
}
