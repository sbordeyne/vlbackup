package openapi

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/op/go-logging"
	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
)

var log = logging.MustGetLogger("vlbackup.openapi")
var logFormat = logging.MustStringFormatter(
	`%{color}%{time:15:04:05.000} %{shortfunc} ▶ %{level:.4s} %{id:03x}%{color:reset} %{message}`,
)

func init() {
	backend := logging.NewLogBackend(os.Stdout, "", 0)
	logging.SetBackend(logging.NewBackendFormatter(backend, logFormat))
}

// Partition names are per-day: YYYYMMDD. Validating this also guarantees the
// name is safe to join into a filesystem path.
var partitionNameRe = regexp.MustCompile(`^\d{8}$`)

// Server implements StrictServerInterface, backing the generated OpenAPI
// handlers with the vlbackup snapshot/transfer/restore logic.
type Server struct {
	args    cli.Args
	metrics *metrics.Metrics
	jobs    *JobStore
}

// NewServer returns a Server wired with the process CLI args and metrics.
func NewServer(args cli.Args, m *metrics.Metrics) *Server {
	return &Server{args: args, metrics: m, jobs: NewJobStore()}
}

var _ StrictServerInterface = (*Server)(nil)

// NewHandler builds the API http.Handler: the generated chi routes fronted by
// a panic recoverer and bearer-auth enforced only on secured operations.
func NewHandler(s StrictServerInterface, authToken string) http.Handler {
	si := NewStrictHandler(s, nil)
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	return HandlerWithOptions(si, ChiServerOptions{
		BaseRouter:  r,
		Middlewares: []MiddlewareFunc{authMiddleware(authToken)},
	})
}

// authMiddleware enforces "Authorization: Bearer <token>" on operations the
// spec marks with bearerAuth. The generated wrapper stamps BearerAuthScopes
// into the request context for exactly those operations before middlewares
// run, so its presence is how we tell secured routes apart. An empty token
// disables the check, matching the optional authKey convention.
func authMiddleware(token string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, secured := r.Context().Value(BearerAuthScopes).([]string); secured && token != "" {
				provided, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
				if !ok || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
					writeError(w, http.StatusUnauthorized, "unauthorized")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeError writes a JSON ErrorResponse with the given status. It is used by
// middleware (which runs outside the strict-handler response machinery);
// operation handlers return typed *JSONResponse objects instead.
func writeError(w http.ResponseWriter, status int, msg string) {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(ErrorResponse{Error: ptr(msg), Code: ptr(int32(status))})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// errorResponse builds an ErrorResponse envelope from an error and status.
func errorResponse(err error, status int) ErrorResponse {
	return ErrorResponse{Error: ptr(err.Error()), Code: ptr(int32(status))}
}

func ptr[T any](v T) *T { return &v }
