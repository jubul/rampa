package auth

import (
	"fmt"

	"rampa.dev/internal/api/store"
)

// Role es un rol por organización.
type Role string

// Roles de mayor a menor.
const (
	RoleOwner     Role = store.RoleOwner
	RoleAdmin     Role = store.RoleAdmin
	RoleDeveloper Role = store.RoleDeveloper
	RoleViewer    Role = store.RoleViewer
)

var roleLevels = map[Role]int{
	RoleViewer:    1,
	RoleDeveloper: 2,
	RoleAdmin:     3,
	RoleOwner:     4,
}

// ParseRole valida un rol que viene de afuera.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if _, ok := roleLevels[r]; !ok {
		return "", fmt.Errorf("rol %q inválido. Opciones: owner, admin, developer, viewer", s)
	}
	return r, nil
}

// Valid dice si el rol existe.
func (r Role) Valid() bool {
	_, ok := roleLevels[r]
	return ok
}

// AtLeast dice si r alcanza al mínimo pedido.
func (r Role) AtLeast(min Role) bool {
	return roleLevels[r] >= roleLevels[min] && roleLevels[r] > 0
}

// Min devuelve el menor de dos roles. Un rol inválido cuenta como el menor.
func Min(a, b Role) Role {
	if roleLevels[a] <= roleLevels[b] {
		return a
	}
	return b
}

// Above devuelve los roles estrictamente superiores a r, como strings para
// pasarlos al store.
func Above(r Role) []string {
	out := []string{}
	for role, lvl := range roleLevels {
		if lvl > roleLevels[r] {
			out = append(out, string(role))
		}
	}
	return out
}

// RequiredRoleForScope devuelve el rol mínimo para desplegar a un scope:
// admin para prod, developer para el resto.
func RequiredRoleForScope(scope string) Role {
	if scope == "prod" {
		return RoleAdmin
	}
	return RoleDeveloper
}
