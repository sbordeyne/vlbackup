package http_ops

import "net/http"

// HealthHandler serves the liveness probe. It is not part of the OpenAPI spec
// and is served on the separate ops port.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}
