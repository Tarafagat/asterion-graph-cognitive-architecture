// Package capability implementa el Capability Router del paper (§ 7.1):
// "AGCA no debería seleccionar plugins por nombre si la tarea puede
// expresarse como una capacidad — Objetivo -> Capacidad requerida ->
// Plugin compatible -> Política -> Ejecución."
//
// Estado real de este MVP, sin adornos: el Registry de acá abajo hace la
// parte de "¿qué proveedor declara esta capacidad?" — el registro es
// manual (Register), NO está conectado todavía al sistema real de
// Asterion Plugins (asterion-core/internal/plugins), que expondría
// capabilities de plugins de verdad instalados y corriendo. Esa conexión
// es Milestone 7 del roadmap del paper ("Capability Registry conectado
// al sistema de Plugins") y requiere que este repo hable con el proceso
// de asterion-core (vía su cliente HTTP, mismo canal que ya usa
// `asterion plugin system apply` para resolver plugins reales) — un paso
// deliberadamente MÁS GRANDE que este esqueleto, y que no se simula acá:
// Match() nunca inventa un proveedor que no fue Register()ado a mano.
package capability

import "sync"

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
