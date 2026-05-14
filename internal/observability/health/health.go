package health

import (
	"encoding/json"
	"net/http"
	"time"
)

type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusUnhealthy Status = "unhealthy"
)

type CheckResult struct {
	Status    Status `json:"status"`
	Timestamp string `json:"timestamp"`
}

type CheckFunc func() error

type Handler struct {
	checks []namedCheck
}

type namedCheck struct {
	Name  string
	Check CheckFunc
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) Register(name string, fn CheckFunc) {
	h.checks = append(h.checks, namedCheck{Name: name, Check: fn})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	overall := StatusHealthy
	details := make(map[string]string, len(h.checks))

	for _, c := range h.checks {
		if err := c.Check(); err != nil {
			overall = StatusUnhealthy
			details[c.Name] = err.Error()
		} else {
			details[c.Name] = "ok"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if overall == StatusUnhealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	json.NewEncoder(w).Encode(CheckResult{
		Status:    overall,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}
