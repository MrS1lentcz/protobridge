package generator

import (
	"fmt"
	"strings"

	"github.com/mrs1lentcz/protobridge/internal/parser"
)

// GenerateEnvExample produces a .env.example file with all supported
// environment variables, comments, and placeholder values.
func GenerateEnvExample(api *parser.ParsedAPI) string {
	var b strings.Builder

	b.WriteString("# =============================================================================\n")
	b.WriteString("# protobridge – Environment Variables\n")
	b.WriteString("# =============================================================================\n")
	b.WriteString("# Copy this file to .env and fill in the values for your environment.\n\n")

	// gRPC targets
	b.WriteString("# --- gRPC service addresses (required) ---\n")
	for _, svc := range api.Services {
		screaming := toScreamingSnake(svc.Name)
		fmt.Fprintf(&b, "PROTOBRIDGE_%s_ADDR=localhost:50051\n", screaming)
	}
	if api.AuthMethod != nil {
		found := false
		for _, svc := range api.Services {
			if svc.Name == api.AuthMethod.ServiceName {
				found = true
				break
			}
		}
		if !found {
			screaming := toScreamingSnake(api.AuthMethod.ServiceName)
			fmt.Fprintf(&b, "PROTOBRIDGE_%s_ADDR=localhost:50051\n", screaming)
		}
	}

	// HTTP server
	b.WriteString("\n# --- HTTP server ---\n")
	b.WriteString("PROTOBRIDGE_PORT=8080\n")

	// TLS
	b.WriteString("\n# --- TLS (HTTPS) ---\n")
	b.WriteString("# PROTOBRIDGE_TLS_CERT=/certs/cert.pem\n")
	b.WriteString("# PROTOBRIDGE_TLS_KEY=/certs/key.pem\n")
	b.WriteString("# PROTOBRIDGE_TLS_SERVER_NAME=api.example.com\n")

	// Per-service TLS
	b.WriteString("\n# --- Per-service gRPC TLS ---\n")
	for _, svc := range api.Services {
		screaming := toScreamingSnake(svc.Name)
		fmt.Fprintf(&b, "# PROTOBRIDGE_%s_TLS=true\n", screaming)
	}

	// CORS
	b.WriteString("\n# --- CORS ---\n")
	b.WriteString("# PROTOBRIDGE_CORS_ORIGINS=https://app.example.com,https://admin.example.com\n")
	b.WriteString("# PROTOBRIDGE_CORS_METHODS=GET,POST,PUT,DELETE,PATCH,OPTIONS\n")
	b.WriteString("# PROTOBRIDGE_CORS_HEADERS=Content-Type,Authorization\n")
	b.WriteString("# PROTOBRIDGE_CORS_MAX_AGE=86400\n")

	// Observability
	b.WriteString("\n# --- Observability ---\n")
	b.WriteString("# PROTOBRIDGE_SENTRY_DSN=https://examplePublicKey@o0.ingest.sentry.io/0\n")
	b.WriteString("# PROTOBRIDGE_OTEL_ENDPOINT=otel-collector:4317\n")
	b.WriteString("# PROTOBRIDGE_OTEL_SERVICE_NAME=protobridge\n")
	b.WriteString("# PROTOBRIDGE_METRICS_PORT=9090\n")

	// gRPC options
	b.WriteString("\n# --- gRPC client options ---\n")
	b.WriteString("# Global options applied to all services.\n")
	b.WriteString("# PROTOBRIDGE_GRPC_OPTIONS=max_recv_msg_size=16mb,keepalive_time=30s,keepalive_timeout=10s\n")
	b.WriteString("#\n")
	b.WriteString("# Per-service overrides (applied on top of global):\n")
	for _, svc := range api.Services {
		screaming := toScreamingSnake(svc.Name)
		fmt.Fprintf(&b, "# PROTOBRIDGE_%s_GRPC_OPTIONS=max_recv_msg_size=64mb\n", screaming)
	}

	return b.String()
}

// GenerateEnvDefaults produces a .env.defaults file with default values
// for all optional environment variables.
func GenerateEnvDefaults(api *parser.ParsedAPI) string {
	var b strings.Builder

	b.WriteString("# protobridge – Default values\n")
	b.WriteString("# These are applied when the variable is not set.\n\n")

	b.WriteString("PROTOBRIDGE_PORT=8080\n")
	b.WriteString("PROTOBRIDGE_OTEL_SERVICE_NAME=protobridge\n")
	b.WriteString("PROTOBRIDGE_CORS_ORIGINS=*\n")
	b.WriteString("PROTOBRIDGE_CORS_METHODS=GET,POST,PUT,DELETE,PATCH,OPTIONS\n")
	b.WriteString("PROTOBRIDGE_CORS_HEADERS=Content-Type,Authorization\n")
	b.WriteString("PROTOBRIDGE_CORS_MAX_AGE=86400\n")

	return b.String()
}
