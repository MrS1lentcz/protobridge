package runtime

import (
	"encoding/json"
	"net/http"
)

type healthResponse struct {
	Status string `json:"status"`
}

// HealthHandler returns an HTTP handler that responds with 200 OK and
// a JSON body {"status":"ok"}. This is the proxy's own liveness/readiness
// endpoint, independent of any backend health service.
//
// Registered automatically at /healthz in every generated proxy.
func HealthHandler() http.HandlerFunc {
	// Pre-marshal at init time. This struct is trivial and cannot fail,
	// but we handle the error for correctness.
	body, err := json.Marshal(healthResponse{Status: "ok"})
	if err != nil {
		body = []byte(`{"status":"ok"}`)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}
