// Package capability implementa el Capability Router del paper (§ 7.1):
// "AGCA no debería seleccionar plugins por nombre si la tarea puede
// expresarse como una capacidad — Objetivo -> Capacidad requerida ->
// Plugin compatible -> Política -> Ejecución."
//
// Estado real de este MVP, sin adornos: el Registry de acá abajo hace la
// parte de "¿qué proveedor declara esta capacidad?". Register es manual
// (nadie lo llama solo) — pero DeriveFromManifest (ver más abajo) ya
// deriva Providers REALES a partir del propio plugin.yaml de un plugin
// (`resources[].crud` + `actions[]`, el mismo contrato que
// `asterion plugin validate` ya valida), no un invento — es lo que
// runtime.Build usa para poblar este Registry a partir de los plugins
// que un Import(...) trajo con una route LOCAL (ver runtime/runtime.go).
// Lo que sigue faltando de Milestone 7 del roadmap del paper
// ("Capability Registry conectado al sistema de Plugins"): un plugin con
// route de git sin clonar, o uno que no vino de ningún Import(...) —
// ninguno de los dos se resuelve todavía; y no hay descubrimiento contra
// procesos de plugin YA CORRIENDO en esta máquina (eso seguiría siendo
// hablar con asterion-core, un paso más grande, no simulado acá).
package capability

import (
	"fmt"
	"sync"

	"github.com/Tarafagat/asterion-plugin-contract/apc"
)

// Provider es quién puede satisfacer una capability — hoy solo un
// identificador (ej. el nombre de un plugin instalado, o "mock:database"
// en una demo) — el paper también pide versión, ubicación, costo y
// permisos para desempatar entre varios candidatos (§ 6 del prompt de
// implementación): quedan para cuando Match() tenga más de un candidato
// real entre los cuales elegir, hoy no fabricado.
type Provider struct {
	Name         string
	Capabilities []string
}

// Registry indexa qué Provider satisface cada capability.
type Registry struct {
	mu        sync.RWMutex
	providers []Provider
}

// NewRegistry crea un Capability Registry vacío.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register agrega un proveedor de capabilities — ver el doc comment del
// paquete: esto es manual en este MVP, no un descubrimiento automático
// contra Asterion Plugins todavía.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
}

// Match devuelve los proveedores registrados que declaran la capability
// pedida — vacío significa "ninguno registrado la cubre", que el caller
// debe tratar como un fallo explícito (ver runtime.CognitiveCycle), nunca
// como "está bien, seguimos sin eso".
func (r *Registry) Match(cap string) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Provider
	for _, p := range r.providers {
		for _, c := range p.Capabilities {
			if c == cap {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// DeriveFromManifest arma un Provider a partir de lo que un plugin YA
// declaró en su propio plugin.yaml — nunca inventa nada nuevo:
//
//   - cada resources[].crud se traduce a "<resource.Name>.<op>" (ej. un
//     resource "invoices" con crud [create, read, list] produce
//     "invoices.create", "invoices.read", "invoices.list" — el mismo
//     estilo <dominio>.<verbo> que usa el paper en sus propios ejemplos:
//     "database.query", "inventory.read");
//   - cada actions[] se traduce a su Name tal cual (ej. una action
//     "issue_invoice" produce la capability "issue_invoice" — una action
//     ya es una operación con nombre propio, no necesita el prefijo de
//     un resource).
//
// Un plugin sin resources ni actions produce un Provider con
// Capabilities vacío — nunca una inferida por otro lado.
func DeriveFromManifest(pluginName string, m apc.Manifest) Provider {
	var caps []string
	for _, r := range m.Resources {
		for _, op := range r.CRUD {
			caps = append(caps, r.Name+"."+op)
		}
	}
	for _, a := range m.Actions {
		caps = append(caps, a.Name)
	}
	return Provider{Name: pluginName, Capabilities: caps}
}

// LoadProviderFromLocalPlugin lee plugin.yaml en dir — apc.LoadManifest
// exige un manifiesto COMPLETO y válido (mismo chequeo que ya hace
// `asterion plugin validate`, nunca una versión relajada solo para
// leer capabilities) — y deriva sus capabilities con DeriveFromManifest.
// Sirve para un plugin con route LOCAL (una carpeta que ya existe en
// disco); uno con route de git sin clonar no tiene plugin.yaml que leer
// todavía — ver runtime.Build, que es quien decide cuál es cuál antes de
// llamar a esto.
func LoadProviderFromLocalPlugin(pluginName, dir string) (Provider, error) {
	m, err := apc.LoadManifest(dir)
	if err != nil {
		return Provider{}, fmt.Errorf("no pude leer el plugin.yaml de %q en %s: %w", pluginName, dir, err)
	}
	return DeriveFromManifest(pluginName, m), nil
}
