package runtime_test

import (
	"testing"

	"github.com/mrs1lentcz/protobridge/runtime"
)

func TestParseGRPCOptions_Empty(t *testing.T) {
	opts, err := runtime.ParseGRPCOptions("")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 0 {
		t.Fatalf("expected 0 opts, got %d", len(opts))
	}
}

func TestParseGRPCOptions_SingleOption(t *testing.T) {
	opts, err := runtime.ParseGRPCOptions("max_recv_msg_size=16mb")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 {
		t.Fatalf("expected 1 opt, got %d", len(opts))
	}
}

func TestParseGRPCOptions_MultipleOptions(t *testing.T) {
	opts, err := runtime.ParseGRPCOptions("max_recv_msg_size=16mb,max_send_msg_size=4mb,keepalive_time=30s,keepalive_timeout=10s,compression=gzip")
	if err != nil {
		t.Fatal(err)
	}
	// max_recv(1) + max_send(1) + keepalive(1) + compression(1) = 4
	if len(opts) != 4 {
		t.Fatalf("expected 4 opts, got %d", len(opts))
	}
}

func TestParseGRPCOptions_SizeParsing(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"max_recv_msg_size=4096", true},
		{"max_recv_msg_size=4kb", true},
		{"max_recv_msg_size=16mb", true},
		{"max_recv_msg_size=1gb", true},
		{"max_recv_msg_size=abc", false},
	}
	for _, tt := range tests {
		_, err := runtime.ParseGRPCOptions(tt.input)
		if tt.valid && err != nil {
			t.Errorf("expected valid for %q, got error: %v", tt.input, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("expected error for %q, got nil", tt.input)
		}
	}
}

func TestParseGRPCOptions_UnknownKey(t *testing.T) {
	_, err := runtime.ParseGRPCOptions("unknown_key=value")
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestParseGRPCOptions_InvalidFormat(t *testing.T) {
	_, err := runtime.ParseGRPCOptions("no_equals_sign")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestParseGRPCOptions_InvalidCompression(t *testing.T) {
	_, err := runtime.ParseGRPCOptions("compression=snappy")
	if err == nil {
		t.Fatal("expected error for unsupported compressor")
	}
}

func TestParseGRPCOptions_Keepalive(t *testing.T) {
	opts, err := runtime.ParseGRPCOptions("keepalive_time=30s,keepalive_timeout=10s,keepalive_permit_without_stream=true")
	if err != nil {
		t.Fatal(err)
	}
	// All keepalive params merge into 1 DialOption
	if len(opts) != 1 {
		t.Fatalf("expected 1 opt (merged keepalive), got %d", len(opts))
	}
}

func TestParseGRPCOptions_WhitespaceHandling(t *testing.T) {
	opts, err := runtime.ParseGRPCOptions("  max_recv_msg_size = 16mb , compression = gzip  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 2 {
		t.Fatalf("expected 2 opts, got %d", len(opts))
	}
}

func TestParseGRPCOptions_MaxSendMsgSize(t *testing.T) {
	opts, err := runtime.ParseGRPCOptions("max_send_msg_size=8mb")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 {
		t.Fatalf("expected 1 opt, got %d", len(opts))
	}
}

func TestParseGRPCOptions_InitialWindowSize(t *testing.T) {
	opts, err := runtime.ParseGRPCOptions("initial_window_size=1mb")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 {
		t.Fatalf("expected 1 opt, got %d", len(opts))
	}
}

func TestParseGRPCOptions_InitialConnWindowSize(t *testing.T) {
	opts, err := runtime.ParseGRPCOptions("initial_conn_window_size=2mb")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 {
		t.Fatalf("expected 1 opt, got %d", len(opts))
	}
}

func TestParseGRPCOptions_CompressionNone(t *testing.T) {
	opts, err := runtime.ParseGRPCOptions("compression=none")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 0 {
		t.Fatalf("expected 0 opts for compression=none, got %d", len(opts))
	}
}

func TestParseGRPCOptions_InvalidKeepaliveTime(t *testing.T) {
	_, err := runtime.ParseGRPCOptions("keepalive_time=invalid")
	if err == nil {
		t.Fatal("expected error for invalid keepalive time")
	}
}

func TestParseGRPCOptions_InvalidKeepaliveTimeout(t *testing.T) {
	_, err := runtime.ParseGRPCOptions("keepalive_timeout=invalid")
	if err == nil {
		t.Fatal("expected error for invalid keepalive timeout")
	}
}

func TestParseGRPCOptions_InvalidKeepalivePermit(t *testing.T) {
	_, err := runtime.ParseGRPCOptions("keepalive_permit_without_stream=maybe")
	if err == nil {
		t.Fatal("expected error for invalid bool")
	}
}

func TestParseGRPCOptions_InvalidWindowSize(t *testing.T) {
	_, err := runtime.ParseGRPCOptions("initial_window_size=abc")
	if err == nil {
		t.Fatal("expected error for invalid window size")
	}
}

func TestParseGRPCOptions_InvalidConnWindowSize(t *testing.T) {
	_, err := runtime.ParseGRPCOptions("initial_conn_window_size=abc")
	if err == nil {
		t.Fatal("expected error for invalid conn window size")
	}
}

func TestParseGRPCOptions_InvalidMaxSendSize(t *testing.T) {
	_, err := runtime.ParseGRPCOptions("max_send_msg_size=abc")
	if err == nil {
		t.Fatal("expected error for invalid max send size")
	}
}

func TestParseGRPCOptions_TrailingComma(t *testing.T) {
	opts, err := runtime.ParseGRPCOptions("max_recv_msg_size=16mb,")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 {
		t.Fatalf("expected 1 opt, got %d", len(opts))
	}
}

func TestParseGRPCOptions_SizeOverflow(t *testing.T) {
	// A size that exceeds MaxInt32 should return an error.
	_, err := runtime.ParseGRPCOptions("max_recv_msg_size=999999gb")
	if err == nil {
		t.Fatal("expected error for size exceeding MaxInt32")
	}
}
