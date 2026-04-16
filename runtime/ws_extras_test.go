package runtime_test

import (
	"testing"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/runtime"
	pb "github.com/mrs1lentcz/protobridge/runtime/testdata"
)

func TestWSAcceptOptions_DefaultReturnsNil(t *testing.T) {
	// All env vars unset, no per-RPC patterns — same-origin default applies.
	t.Setenv("PROTOBRIDGE_WS_ORIGIN_PATTERNS", "")
	t.Setenv("PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY", "")
	t.Setenv("PROTOBRIDGE_ENV", "")

	if got := runtime.WSAcceptOptions(runtime.WSAcceptConfig{}); got != nil {
		t.Fatalf("expected nil AcceptOptions, got %+v", got)
	}
}

func TestWSAcceptOptions_UnionsPerRPCAndEnv(t *testing.T) {
	t.Setenv("PROTOBRIDGE_WS_ORIGIN_PATTERNS", "env.example.com, shared.example.com")
	t.Setenv("PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY", "")
	t.Setenv("PROTOBRIDGE_ENV", "")

	got := runtime.WSAcceptOptions(runtime.WSAcceptConfig{
		PerRPCPatterns: "rpc.example.com,shared.example.com",
	})
	if got == nil {
		t.Fatal("expected non-nil AcceptOptions")
	}
	want := []string{"rpc.example.com", "shared.example.com", "env.example.com"}
	if len(got.OriginPatterns) != len(want) {
		t.Fatalf("patterns = %v, want %v", got.OriginPatterns, want)
	}
	for i, p := range want {
		if got.OriginPatterns[i] != p {
			t.Fatalf("pattern[%d] = %q, want %q", i, got.OriginPatterns[i], p)
		}
	}
}

func TestWSAcceptOptions_InsecureSkipVerifyWinsInDev(t *testing.T) {
	t.Setenv("PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY", "true")
	t.Setenv("PROTOBRIDGE_WS_ORIGIN_PATTERNS", "example.com")
	t.Setenv("PROTOBRIDGE_ENV", "development")

	got := runtime.WSAcceptOptions(runtime.WSAcceptConfig{PerRPCPatterns: "a.com"})
	if got == nil || !got.InsecureSkipVerify {
		t.Fatalf("expected InsecureSkipVerify=true, got %+v", got)
	}
	if len(got.OriginPatterns) != 0 {
		t.Fatalf("skip-verify implies no pattern check, got %v", got.OriginPatterns)
	}
}

func TestWSAcceptOptions_EmptyPatternsAfterTrim(t *testing.T) {
	t.Setenv("PROTOBRIDGE_WS_ORIGIN_PATTERNS", " , , ,")
	t.Setenv("PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY", "")
	t.Setenv("PROTOBRIDGE_ENV", "")

	if got := runtime.WSAcceptOptions(runtime.WSAcceptConfig{PerRPCPatterns: " ,  "}); got != nil {
		t.Fatalf("all-blank lists should collapse to nil, got %+v", got)
	}
}

func TestInitWSConfig_AllowsDev(t *testing.T) {
	// Skip-verify in a dev/local environment is the documented escape
	// hatch — InitWSConfig must return without panic.
	t.Setenv("PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY", "true")
	t.Setenv("PROTOBRIDGE_ENV", "development")
	runtime.InitWSConfig()

	t.Setenv("PROTOBRIDGE_ENV", "")
	runtime.InitWSConfig()
}

func TestInitWSConfig_NoOpWhenSkipVerifyOff(t *testing.T) {
	// When the escape hatch isn't engaged InitWSConfig must bail before
	// touching PROTOBRIDGE_ENV — "production" must not be a tripwire by
	// itself.
	t.Setenv("PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY", "")
	t.Setenv("PROTOBRIDGE_ENV", "production")
	runtime.InitWSConfig()
}

func TestInitWSConfig_PanicsInProduction(t *testing.T) {
	t.Setenv("PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY", "true")
	t.Setenv("PROTOBRIDGE_ENV", "production")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when skip-verify meets production")
		}
		if msg, _ := r.(string); msg != runtime.ErrWSInsecureInProduction {
			t.Fatalf("panic = %v, want %q", r, runtime.ErrWSInsecureInProduction)
		}
	}()
	runtime.InitWSConfig()
}

func TestWSAcceptOptions_PanicsOnProductionSkipVerify(t *testing.T) {
	// Mirrors InitWSConfig's guard as defence in depth — a custom bootstrap
	// that skipped InitWSConfig still can't silently downgrade to an
	// open-origin upgrade in production.
	t.Setenv("PROTOBRIDGE_WS_INSECURE_SKIP_VERIFY", "true")
	t.Setenv("PROTOBRIDGE_ENV", "PRODUCTION") // case-insensitive
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on skip-verify in production")
		}
	}()
	_ = runtime.WSAcceptOptions(runtime.WSAcceptConfig{})
}

func TestUnmarshalWSFrame_Text(t *testing.T) {
	// Text frames go through protojson, which accepts the JSON-encoded form.
	payload := []byte(`{"name":"txt","age":7}`)
	var out pb.SimpleRequest
	if err := runtime.UnmarshalWSFrame(websocket.MessageText, payload, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Name != "txt" || out.Age != 7 {
		t.Fatalf("got %+v, want name=txt age=7", &out)
	}
}

func TestUnmarshalWSFrame_Binary(t *testing.T) {
	src := &pb.SimpleRequest{Name: "payload", Age: 42}
	data, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out pb.SimpleRequest
	if err := runtime.UnmarshalWSFrame(websocket.MessageBinary, data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Name != "payload" || out.Age != 42 {
		t.Fatalf("got %+v, want name=payload age=42", &out)
	}
}

func TestUnmarshalWSFrame_TextRejectsBinaryBytes(t *testing.T) {
	src := &pb.SimpleRequest{Name: "payload"}
	data, _ := proto.Marshal(src)
	var out pb.SimpleRequest
	// Calling the text branch with raw proto bytes must surface a JSON parse
	// error rather than silently producing a zero value.
	if err := runtime.UnmarshalWSFrame(websocket.MessageText, data, &out); err == nil {
		t.Fatal("expected protojson error on binary bytes framed as text")
	}
}
