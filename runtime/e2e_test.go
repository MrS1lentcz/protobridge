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

	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

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

	return r
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: expected 200, got %d", i, resp.StatusCode)
		}
	}
}
