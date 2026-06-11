package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type problemDetails struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Status   int            `json:"status"`
	Detail   string         `json:"detail"`
	Instance string         `json:"instance,omitempty"`
	Errors   []problemError `json:"errors,omitempty"`
}

func (s *server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	s.writeProblem(w, r, problemDetails{
		Type:     "about:blank",
		Title:    http.StatusText(http.StatusNotFound),
		Status:   http.StatusNotFound,
		Detail:   "API route is not implemented",
		Instance: r.URL.RequestURI(),
	})
}

func (s *server) writeProblem(w http.ResponseWriter, r *http.Request, problem problemDetails) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(problem.Status)

	if err := json.NewEncoder(w).Encode(problem); err != nil {
		s.log.ErrorContext(r.Context(), "encode problem response", slog.Any("error", err))
	}
}
