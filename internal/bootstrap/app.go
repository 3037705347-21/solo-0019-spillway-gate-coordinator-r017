package bootstrap

import (
	"net/http"
	"os"
	"strings"

	"example.com/spillway-gate-coordinator/internal/coordination"
	"example.com/spillway-gate-coordinator/internal/httpapi"
	"example.com/spillway-gate-coordinator/internal/storage"
)

// NewHandler wires the in-memory repository, coordination service, and HTTP
// router. The authorized-maintainer allow list defaults to the built-in one
// and can be overridden with GATE_MAINTAINERS (comma-separated identities).
func NewHandler() http.Handler {
	repository := storage.NewMemoryRepository()

	maintainers := coordination.DefaultMaintainers()
	if configured := strings.TrimSpace(os.Getenv("GATE_MAINTAINERS")); configured != "" {
		maintainers = strings.Split(configured, ",")
	}
	service := coordination.NewServiceWithOptions(repository, maintainers)
	return httpapi.NewRouter(service)
}
