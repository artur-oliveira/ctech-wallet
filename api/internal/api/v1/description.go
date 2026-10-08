package v1

import (
	"strings"

	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// DescriptionMinLen is the shortest acceptable description once the
// REQUIRE_DESCRIPTION flag is on (after trimming).
const DescriptionMinLen = 3

// checkDescription enforces the mandatory-description rule. A nil result means
// the description is acceptable, or the flag is off (legacy optional behavior).
// The max length is enforced by the DTO validator (`max=255`), so an oversize
// description is a 400, never silently truncated.
func (h *handlers) checkDescription(desc string) *problem.Problem {
	if !h.requireDescription {
		return nil
	}
	if len(strings.TrimSpace(desc)) < DescriptionMinLen {
		return problem.BadRequest("description é obrigatório (mínimo 3 caracteres)")
	}
	return nil
}
