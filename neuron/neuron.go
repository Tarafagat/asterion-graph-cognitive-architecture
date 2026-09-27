// Package neuron implementa el Neuron Contract del paper (§ 5): una
// unidad computacional intercambiable que transforma información bajo un
// contrato estable, model-agnostic por diseño (§ 5.3) — el runtime no
// codifica proveedores concretos, solo depende de la interfaz Neuron.
//
// Este MVP trae UN adaptador real y funcional: DeterministicNeuron (§
// 23.2, MVP 2: "registrar una neurona determinista"). GGUFNeuron
// (llama.cpp) y RemoteLLMNeuron (adapter genérico a un proveedor remoto)
// son Milestone 9/10 del roadmap del paper — deliberadamente NO
// implementados acá: agregar uno hoy sería simular una integración que
// nadie verificó en vivo, exactamente lo que este proyecto evita en
// cualquier otro repo del ecosistema (ver la disciplina de
// "todo lo documentado está corrido de verdad" en
// asterion-language/docs/TUTORIAL.md). Sus manifiestos SÍ pueden
// declararse desde un .asterion (AGCA.neuron(runtime="gguf", ...) o
// AGCA.neuron(adapter="remote-llm", ...)) — Registry.Match los expone
// como candidatos igual, pero invocarlos hoy devuelve ErrNotImplemented
// en vez de fallar en silencio o fingir una respuesta.
package neuron

import (
	"context"
	"errors"
	"sync"
)

// ErrNotImplemented lo devuelve cualquier Neuron cuyo adaptador real
// todavía no está construido en este repo (ver doc comment del paquete).
var ErrNotImplemented = errors.New("neuron: adaptador no implementado todavía en este runtime")

// Manifest es el contrato mínimo que el paper pide en § 5.2 — identidad,
// capacidades, privacidad y salud. Cost/Latency/Reputation quedan para
// cuando exista experience.Store con datos reales que agregar (Milestone
// 8+ del roadmap) — declararlos acá ahora sería inventar números.
type Manifest struct {
	Name         string
	Provider     string // ej. "local-gguf", "remote-llm", "deterministic"
	Capabilities []string
	Privacy      string // "local" | "remote"
	Healthy      bool
}

// Neuron es el contrato estable — Ni: (x, mi) -> (y, mi') del paper (§ 5),
// simplificado para este MVP a una transformación sin estado propio
// persistente entre llamadas (el estado, si hace falta, vive en el
// Cognitive Graph, no adentro de la neurona).
type Neuron interface {
	Manifest() Manifest
	Transform(ctx context.Context, input string) (output string, err error)
}

// Registry indexa neuronas registradas y las hace elegibles por
// capacidad — § 11 del paper (Cognitive Router: "debe elegir neuronas
// por capacidad, no por marca"). El scoring real (reputación, costo,
// latencia, privacidad ponderada) es Milestone 11 del roadmap — este MVP
// hace el primer paso honesto: filtrar por capacidad y por salud, y
// devolver locales antes que remotas (única heurística fija, documentada
// como tal, no una fórmula de score inventada).
type Registry struct {
	mu      sync.RWMutex
	neurons []Neuron
}

// NewRegistry crea un Neuron Registry vacío.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register agrega una neurona al registry. No valida duplicados por
// nombre a propósito: dos neuronas con manifiestos distintos pero mismo
// Name (ej. una versión vieja y una nueva conviviendo durante un
// despliegue) son un caso legítimo — desambiguar cuál usar es trabajo del
// scoring de Match, no de Register.
func (r *Registry) Register(n Neuron) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.neurons = append(r.neurons, n)
}

// Match devuelve las neuronas registradas que declaran la capability
// pedida, saludables primero... en realidad: TODAS las saludables (las no
// saludables se descartan), ordenadas con locales antes que remotas —
// ver doc comment del tipo. Vacío significa "ninguna neurona registrada
// cubre esto", nunca un error: decidir qué hacer en ese caso (escalar,
// fallar, pedir un plugin en cambio) es responsabilidad del caller.
func (r *Registry) Match(capability string) []Neuron {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var local, remote []Neuron
	for _, n := range r.neurons {
		m := n.Manifest()
		if !m.Healthy {
			continue
		}
		if !hasCapability(m.Capabilities, capability) {
			continue
		}
		if m.Privacy == "remote" {
			remote = append(remote, n)
		} else {
			local = append(local, n)
		}
	}
	return append(local, remote...)
}

// All devuelve TODAS las neuronas registradas, saludables o no — a
// diferencia de Match, que filtra por salud y capacidad para elegir a
// quién invocar, All existe para inspección/listado (ej. 'agca inspect':
// mostrar también lo declarado-pero-no-disponible, en vez de que
// desaparezca en silencio).
func (r *Registry) All() []Neuron {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Neuron, len(r.neurons))
	copy(out, r.neurons)
	return out
}

func hasCapability(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}
