package runtime

import (
	"context"
	"log"
	"net/http"

	"github.com/mrs1lentcz/gox/errorx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SentryMiddleware returns a chi-compatible middleware that recovers panics
// and reports them via the global error reporter.
func SentryMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					err, ok := rec.(error)
					if !ok {
						err = &panicError{value: rec}
					}
					reportError(err)
					WriteError(w, http.StatusInternalServerError, "INTERNAL", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// reportError sends an error to Sentry (or the configured error reporter).
// Use this ONLY for errors that indicate bugs or serious issues requiring
// investigation: panics, data corruption, unexpected server-side failures.
//
// Do NOT use for: client disconnects, stream EOF, transient gRPC errors,
// context cancellation, or normal lifecycle events.
func reportError(err error) {
	errorx.ReportError(err)
}

// logError logs a minor/expected error to stderr. Use this for errors that
// are part of normal operation: client disconnects, stream lifecycle events,
// transient network issues. These should NOT go to Sentry.
func logError(err error) {
	log.Printf("error: %v", err)
}

// isClientGone returns true if the error indicates the client disconnected
// or cancelled the request. These are normal lifecycle events, not bugs.
func isClientGone(err error) bool {
	if err == nil {
		return false
	}
	if err == context.Canceled || err == context.DeadlineExceeded {
		return true
	}
	// gRPC cancelled = client went away.
	if s, ok := status.FromError(err); ok && s.Code() == codes.Canceled {
		return true
	}
	return false
}

// recoverGoroutine should be deferred at the top of every goroutine spawned
// by the runtime. It recovers panics and reports them to Sentry instead of
// crashing the process.
func recoverGoroutine() {
	if rec := recover(); rec != nil {
		err, ok := rec.(error)
		if !ok {
			err = &panicError{value: rec}
		}
		reportError(err)
	}
}

type panicError struct {
	value any
}

func (e *panicError) Error() string {
	return "panic: " + stringify(e.value)
}

func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return "unknown panic"
}
