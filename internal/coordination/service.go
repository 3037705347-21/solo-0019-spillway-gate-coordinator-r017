package coordination

import (
	"strings"
	"time"

	"example.com/spillway-gate-coordinator/internal/domain"
	"example.com/spillway-gate-coordinator/internal/storage"
)

// defaultMaintainers is the built-in allow list of operators authorized to
// clear a latched gate fault. The list can be replaced via
// NewServiceWithOptions.
var defaultMaintainers = []string{
	"maintainer-lu",
	"maintainer-zheng",
	"maintainer-chen",
}

// DefaultMaintainers returns a copy of the built-in maintainer allow list.
func DefaultMaintainers() []string {
	maintainers := make([]string, len(defaultMaintainers))
	copy(maintainers, defaultMaintainers)
	return maintainers
}

type Service struct {
	repository  storage.Repository
	maintainers map[string]struct{}
	now         func() time.Time
	newID       func(prefix string) (string, error)
	newToken    func() (string, error)
}

func NewService(repository storage.Repository) *Service {
	return NewServiceWithOptions(repository, defaultMaintainers)
}

// NewServiceWithOptions builds a service with an explicit maintainer allow
// list (mainly used by bootstrap configuration and tests).
func NewServiceWithOptions(repository storage.Repository, maintainers []string) *Service {
	allowList := make(map[string]struct{}, len(maintainers))
	for _, maintainer := range maintainers {
		maintainer = strings.TrimSpace(maintainer)
		if maintainer != "" {
			allowList[maintainer] = struct{}{}
		}
	}
	return &Service{
		repository:  repository,
		maintainers: allowList,
		now:         time.Now,
		newID:       randomIdentifier,
		newToken:    randomToken,
	}
}

// authorizeMaintainer enforces that fault clearance can only be requested by
// a configured maintainer identity.
func (s *Service) authorizeMaintainer(identity string) error {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return domain.Invalid("invalid_handled_by", "handled_by is required")
	}
	if _, allowed := s.maintainers[identity]; !allowed {
		return domain.Forbidden("maintainer_not_authorized", "operator is not authorized to clear gate faults")
	}
	return nil
}
