package runtime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Tarafagat/asterion-language/agcaspec"

	"github.com/Tarafagat/asterion-graph-cognitive-architecture/cognition"
)

// Role es un rol YA RESUELTO: su cadena de herencia aplanada, con el
// conjunto efectivo de capabilities permitidas y denegadas.
//
// La regla de composición es la única segura posible: DENY GANA SIEMPRE.
// Si cualquier rol de la cadena (propio o heredado) deniega una
// capability, ningún allow de otro rol la reabre. Heredar amplía lo que
// se puede hacer, nunca lo que estaba prohibido.
type Role struct {
	Name        string
	Description string
	// Chain es la cadena de herencia resuelta, de la raíz hasta este rol
	// — para poder explicar de dónde salió cada permiso.
	Chain []string
	Allow []string
	Deny  []string
	Users []string
}

// Allows responde si este rol autoriza una capability, aplicando deny
// primero.
func (r Role) Allows(capabilityID string) (bool, string) {
	for _, d := range r.Deny {
		if d == capabilityID {
			return false, fmt.Sprintf("el rol %q deniega explícitamente %q", r.Name, capabilityID)
		}
	}
	for _, a := range r.Allow {
		if a == capabilityID {
			return true, ""
		}
	}
	return false, fmt.Sprintf("el rol %q no tiene %q entre sus capabilities (deny-by-default)", r.Name, capabilityID)
}

// Roles devuelve todos los roles de esta Intelligence ya resueltos,
// ordenados por nombre.
func (rt *Runtime) Roles() ([]Role, error) {
	names := make([]string, 0, len(rt.RoleDecls))
	for _, r := range rt.RoleDecls {
		names = append(names, r.VarName)
	}
	sort.Strings(names)

	out := make([]Role, 0, len(names))
	for _, n := range names {
		role, err := rt.ResolveRole(n)
		if err != nil {
			return nil, err
		}
		out = append(out, role)
	}
	return out, nil
}

// ResolveRole aplana la cadena de herencia de un rol. Acepta tanto el
// nombre de variable del .asterion como el Name declarado — quien usa el
// CLI piensa en "analyst", no en cómo se llamó la variable.
func (rt *Runtime) ResolveRole(nameOrVar string) (Role, error) {
	decl, ok := rt.findRoleDecl(nameOrVar)
	if !ok {
		return Role{}, fmt.Errorf("runtime: esta intelligence no declara ningún AGCA.role(...) llamado %q — declarados: %s",
			nameOrVar, strings.Join(rt.roleNames(), ", "))
	}

	resolved := Role{Name: decl.Name, Description: decl.Description, Users: decl.Users}
	seen := map[string]bool{}
	allow := map[string]bool{}
	deny := map[string]bool{}

	// visit recorre la cadena en profundidad. El ciclo se detecta acá y
	// es un error explícito: un rol que se hereda a sí mismo (directa o
	// indirectamente) haría indefinida la autoridad efectiva.
	var visit func(varName string, path []string) error
	visit = func(varName string, path []string) error {
		for _, p := range path {
			if p == varName {
				return fmt.Errorf("runtime: ciclo de herencia de roles: %s -> %s",
					strings.Join(path, " -> "), varName)
			}
		}
		if seen[varName] {
			return nil // ya incorporado por otra rama; no es un ciclo
		}
		seen[varName] = true

		d, ok := rt.findRoleDecl(varName)
		if !ok {
			return fmt.Errorf("runtime: el rol %q hereda de %q, que no está declarado", decl.Name, varName)
		}
		for _, parent := range d.Inherits {
			if err := visit(parent, append(path, varName)); err != nil {
				return err
			}
		}
		resolved.Chain = append(resolved.Chain, d.Name)
		for _, a := range d.Allow {
			allow[a] = true
		}
		for _, x := range d.Deny {
			deny[x] = true
		}
		return nil
	}
	if err := visit(decl.VarName, nil); err != nil {
		return Role{}, err
	}

	// Deny gana: una capability denegada en cualquier punto de la cadena
	// sale del allow efectivo, no queda "permitida por el hijo".
	for a := range allow {
		if !deny[a] {
			resolved.Allow = append(resolved.Allow, a)
		}
	}
	for d := range deny {
		resolved.Deny = append(resolved.Deny, d)
	}
	sort.Strings(resolved.Allow)
	sort.Strings(resolved.Deny)
	return resolved, nil
}

// RoleForUser resuelve qué rol tiene un usuario concreto, según los
// users=[...] declarados. Si un usuario figura en más de un rol, es un
// error explícito: elegir uno por nosotros sería decidir en silencio
// cuánta autoridad tiene alguien.
func (rt *Runtime) RoleForUser(user string) (Role, error) {
	var matches []string
	for _, d := range rt.RoleDecls {
		for _, u := range d.Users {
			if u == user {
				matches = append(matches, d.VarName)
				break
			}
		}
	}
	switch len(matches) {
	case 0:
		return Role{}, fmt.Errorf("runtime: ningún AGCA.role(...) declara a %q entre sus users — sin rol no hay autoridad (deny-by-default)", user)
	case 1:
		return rt.ResolveRole(matches[0])
	default:
		return Role{}, fmt.Errorf("runtime: %q figura en %d roles (%s) — indicá cuál usar con --role en vez de dejar que se elija solo",
			user, len(matches), strings.Join(matches, ", "))
	}
}

func (rt *Runtime) findRoleDecl(nameOrVar string) (agcaspec.RoleDecl, bool) {
	for _, r := range rt.RoleDecls {
		if r.VarName == nameOrVar || r.Name == nameOrVar {
			return r, true
		}
	}
	return agcaspec.RoleDecl{}, false
}

func (rt *Runtime) roleNames() []string {
	out := make([]string, 0, len(rt.RoleDecls))
	for _, r := range rt.RoleDecls {
		out = append(out, r.Name)
	}
	sort.Strings(out)
	return out
}

// composedPermissions compone la autoridad del ROL con la del AGENTE.
// Ninguna de las dos puede ampliar a la otra: hace falta que las DOS
// permitan (intersección), y cualquiera de las dos puede denegar.
//
// Esto es lo que hace que el mismo agente, operado por un viewer o por
// un admin, no pueda hacer las mismas cosas — que es justamente el punto
// de tener roles además de agentes.
type composedPermissions struct {
	role  *Role
	agent cognition.PermissionGate
}

func (c *composedPermissions) Permitted(capabilityID string) (bool, string) {
	if c.role != nil {
		if allowed, reason := c.role.Allows(capabilityID); !allowed {
			return false, reason
		}
	}
	if c.agent != nil {
		if allowed, reason := c.agent.Permitted(capabilityID); !allowed {
			return false, reason
		}
	}
	if c.role == nil && c.agent == nil {
		return false, "sin rol ni agente no hay autoridad para ninguna capability (deny-by-default)"
	}
	return true, ""
}
