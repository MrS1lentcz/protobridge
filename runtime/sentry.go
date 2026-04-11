package runtime

import (
	"net/http"

	"github.com/mrs1lentcz/gox/errorx"
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

// reportError sends an error to the global error reporter (Sentry or stderr).
func reportError(err error) {
	errorx.ReportError(err)
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
