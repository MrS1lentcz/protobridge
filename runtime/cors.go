package runtime

import (
	"net/http"
	"os"
	"strconv"
	"strings"
)

// CORSConfig holds CORS configuration parsed from environment variables.
type CORSConfig struct {
	AllowOrigins []string // PROTOBRIDGE_CORS_ORIGINS (comma-separated, default: *)
	AllowMethods []string // PROTOBRIDGE_CORS_METHODS (comma-separated, default: GET,POST,PUT,DELETE,PATCH,OPTIONS)
	AllowHeaders []string // PROTOBRIDGE_CORS_HEADERS (comma-separated, default: Content-Type,Authorization)
	MaxAge       int      // PROTOBRIDGE_CORS_MAX_AGE (seconds, default: 86400)
}

// CORSConfigFromEnv reads CORS configuration from environment variables.
func CORSConfigFromEnv() CORSConfig {
	cfg := CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
		AllowHeaders: []string{"Content-Type", "Authorization"},
		MaxAge:       86400,
	}

	if origins := os.Getenv("PROTOBRIDGE_CORS_ORIGINS"); origins != "" {
		cfg.AllowOrigins = splitTrim(origins)
	}
	if methods := os.Getenv("PROTOBRIDGE_CORS_METHODS"); methods != "" {
		cfg.AllowMethods = splitTrim(methods)
	}
	if headers := os.Getenv("PROTOBRIDGE_CORS_HEADERS"); headers != "" {
		cfg.AllowHeaders = splitTrim(headers)
	}
	if maxAge := os.Getenv("PROTOBRIDGE_CORS_MAX_AGE"); maxAge != "" {
		if n, err := strconv.Atoi(maxAge); err == nil {
			cfg.MaxAge = n
		}
	}

	return cfg
}

// CORSMiddleware returns a chi-compatible middleware that handles CORS
// preflight and response headers.
func CORSMiddleware(cfg CORSConfig) func(http.Handler) http.Handler {
	methods := strings.Join(cfg.AllowMethods, ", ")
	headers := strings.Join(cfg.AllowHeaders, ", ")
	maxAge := strconv.Itoa(cfg.MaxAge)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed := false
			for _, o := range cfg.AllowOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}

			if !allowed {
				next.ServeHTTP(w, r)
				return
			}

			// Set CORS headers
			if len(cfg.AllowOrigins) == 1 && cfg.AllowOrigins[0] == "*" {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", methods)
			w.Header().Set("Access-Control-Allow-Headers", headers)
			w.Header().Set("Access-Control-Max-Age", maxAge)

			// Preflight
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
