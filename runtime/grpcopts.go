package runtime

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/keepalive"
)

// ParseGRPCOptions parses a comma-separated key=value string into gRPC
// DialOptions. This allows configuring gRPC client behavior via environment
// variables.
//
// Supported keys:
//
//	max_recv_msg_size  – maximum message size the client can receive (e.g. "16mb", "4096")
//	max_send_msg_size  – maximum message size the client can send
//	keepalive_time     – interval between keepalive pings (Go duration, e.g. "30s")
//	keepalive_timeout  – timeout waiting for keepalive ping ack (e.g. "10s")
//	keepalive_permit_without_stream – allow keepalive without active streams ("true"/"false")
//	initial_window_size      – per-stream flow control window (e.g. "1mb")
//	initial_conn_window_size – per-connection flow control window (e.g. "2mb")
//	compression               – default compressor ("gzip" or "none")
//
// Size values accept human-readable suffixes: kb, mb, gb (case-insensitive).
// Values without suffix are treated as bytes.
//
// Example:
//
//	"max_recv_msg_size=16mb,keepalive_time=30s,keepalive_timeout=10s,compression=gzip"
func ParseGRPCOptions(s string) ([]grpc.DialOption, error) {
	if s == "" {
		return nil, nil
	}

	var opts []grpc.DialOption
	var kp keepalive.ClientParameters
	hasKeepalive := false

	pairs := strings.Split(s, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("grpc option: invalid format %q (expected key=value)", pair)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "max_recv_msg_size":
			size, err := parseSize(val)
			if err != nil {
				return nil, fmt.Errorf("grpc option %s: %w", key, err)
			}
			opts = append(opts, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(size)))

		case "max_send_msg_size":
			size, err := parseSize(val)
			if err != nil {
				return nil, fmt.Errorf("grpc option %s: %w", key, err)
			}
			opts = append(opts, grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(size)))

		case "keepalive_time":
			d, err := time.ParseDuration(val)
			if err != nil {
				return nil, fmt.Errorf("grpc option %s: %w", key, err)
			}
			kp.Time = d
			hasKeepalive = true

		case "keepalive_timeout":
			d, err := time.ParseDuration(val)
			if err != nil {
				return nil, fmt.Errorf("grpc option %s: %w", key, err)
			}
			kp.Timeout = d
			hasKeepalive = true

		case "keepalive_permit_without_stream":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return nil, fmt.Errorf("grpc option %s: %w", key, err)
			}
			kp.PermitWithoutStream = b
			hasKeepalive = true

		case "initial_window_size":
			size, err := parseSize(val)
			if err != nil {
				return nil, fmt.Errorf("grpc option %s: %w", key, err)
			}
			opts = append(opts, grpc.WithInitialWindowSize(int32(size)))

		case "initial_conn_window_size":
			size, err := parseSize(val)
			if err != nil {
				return nil, fmt.Errorf("grpc option %s: %w", key, err)
			}
			opts = append(opts, grpc.WithInitialConnWindowSize(int32(size)))

		case "compression":
			switch strings.ToLower(val) {
			case "gzip":
				opts = append(opts, grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name)))
			case "none", "":
				// no-op
			default:
				return nil, fmt.Errorf("grpc option %s: unsupported compressor %q (use \"gzip\" or \"none\")", key, val)
			}

		default:
			return nil, fmt.Errorf("grpc option: unknown key %q", key)
		}
	}

	if hasKeepalive {
		opts = append(opts, grpc.WithKeepaliveParams(kp))
	}

	return opts, nil
}

// parseSize parses a human-readable size string into bytes.
// Accepts: "1024", "4kb", "16mb", "1gb" (case-insensitive).
func parseSize(s string) (int, error) {
	s = strings.TrimSpace(strings.ToLower(s))

	multiplier := 1
	if strings.HasSuffix(s, "gb") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "gb")
	} else if strings.HasSuffix(s, "mb") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "mb")
	} else if strings.HasSuffix(s, "kb") {
		multiplier = 1024
		s = strings.TrimSuffix(s, "kb")
	}

	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}

	result := n * float64(multiplier)
	if result > math.MaxInt32 {
		return 0, fmt.Errorf("size %q exceeds maximum", s)
	}

	return int(result), nil
}
