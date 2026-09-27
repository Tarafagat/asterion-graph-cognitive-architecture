// Package graph implementa el Cognitive Graph propuesto en "Camino a la
// AGI" § 4: Ct = (Vt, Eg_t, Θt) — nodos, relaciones tipadas y metadatos
// (confianza, procedencia, temporalidad). Es el MVP 1 (Graph Runtime) del
// documento: crear nodos/relaciones tipadas, subgrafos jerárquicos,
// mantener confianza/temporalidad/procedencia, transacciones/snapshots, y
// consultar por patrón/capacidad.
//
// Deliberadamente en memoria (map + mutex) — un GraphStore real
// respaldado en disco (SQLite/embebido, como sugiere § 21 del paper) es
// una extensión futura de este mismo Store interface, no algo que este
// MVP necesite para demostrar la composición. Ver README.md § "Qué falta
// a propósito".
package graph

import (
	"fmt"
	"sync"
	"time"
)

// Node es una unidad cognitiva — puede representar una característica
// completa (§ 4.1 del paper: una curva, un objeto, una hipótesis, un
// embedding) o, en este runtime, una Observación/Insight producida por un
// ciclo cognitivo. Payload es deliberadamente `any`: el store no le
// impone estructura al contenido, solo lo indexa y lo versiona.
type Node struct {
	ID         string
	Kind       string // ej. "observation", "insight", "hypothesis"
	Payload    any
	Confidence float64
	Provenance Provenance
	Tags       []string
	CreatedAt  time.Time
	Version    int
}

// Provenance responde "quién lo creó, cuándo, desde qué fuente, con qué
// evidencia" — § 10 del paper. TraceID conecta un nodo con el ciclo
// cognitivo completo que lo produjo (runtime.CognitiveCycle).
type Provenance struct {
	Source  string // ej. "neuron:LocalFast", "plugin:database.query", "user"
	TraceID string
}

// Edge es una relación tipada entre dos nodos — puede llevar su propia
// confianza/procedencia (§ 4 del paper: "los edges deben ser tipados y
// poder tener confidence/provenance").
type Edge struct {
	From       string
	To         string
	Kind       string // ej. "derived_from", "contradicts", "supports"
	Confidence float64
	Provenance Provenance
}

// Snapshot es una foto congelada del grafo en un momento — ver
// Store.Snapshot. Se usa para auditar un ciclo cognitivo completo sin que
// escrituras concurrentes posteriores lo alteren retroactivamente.
type Snapshot struct {
	Nodes []Node
	Edges []Edge
	Taken time.Time
}

// Store es un Cognitive Graph en memoria, seguro para uso concurrente —
// varios agentes/neuronas pueden leer y escribir al mismo tiempo (mismo
// supuesto que "miles de pequeñas inteligencias operan sobre ese mundo",
// Principio Cognitivo I del paper).
type Store struct {
	mu    sync.RWMutex
	nodes map[string]Node
	edges []Edge
}

// NewStore crea un Cognitive Graph vacío.
func NewStore() *Store {
	return &Store{nodes: map[string]Node{}}
}

// AddNode inserta o actualiza un nodo. Si ID ya existía, Version se
// incrementa (nunca se pierde el número de la versión anterior) — la
// forma mínima de temporalidad que pide el paper sin necesitar guardar
// cada versión histórica completa todavía (eso es Snapshot, más abajo).
func (s *Store) AddNode(n Node) Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.nodes[n.ID]; ok {
		n.Version = existing.Version + 1
	} else {
		n.Version = 1
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	s.nodes[n.ID] = n
	return n
}

// AddEdge inserta una relación tipada entre dos nodos ya existentes.
// Devuelve error si From/To no existen — un edge nunca puede colgar de
// un nodo fantasma.
func (s *Store) AddEdge(e Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[e.From]; !ok {
		return fmt.Errorf("graph: AddEdge: nodo origen %q no existe", e.From)
	}
	if _, ok := s.nodes[e.To]; !ok {
		return fmt.Errorf("graph: AddEdge: nodo destino %q no existe", e.To)
	}
	s.edges = append(s.edges, e)
	return nil
}

// Node busca un nodo por ID.
func (s *Store) Node(id string) (Node, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.nodes[id]
	return n, ok
}

// EdgesFrom devuelve todas las relaciones que salen de un nodo — la
// forma mínima de "expandir un subgrafo" (§ 4.3 del paper: resolución
// cognitiva, expandir solo cuando hace falta).
func (s *Store) EdgesFrom(id string) []Edge {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Edge
	for _, e := range s.edges {
		if e.From == id {
			out = append(out, e)
		}
	}
	return out
}

// ByKind devuelve todos los nodos de un Kind dado — la forma mínima de
// "consultar subgrafos por patrón" que pide el MVP 1 del paper.
func (s *Store) ByKind(kind string) []Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Node
	for _, n := range s.nodes {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

// Snapshot congela el estado actual del grafo — usado para auditar un
// ciclo cognitivo (runtime.CognitiveCycle) contra el estado exacto que
// vio, sin que escrituras concurrentes posteriores lo contaminen.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nodes := make([]Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		nodes = append(nodes, n)
	}
	edges := make([]Edge, len(s.edges))
	copy(edges, s.edges)
	return Snapshot{Nodes: nodes, Edges: edges, Taken: time.Now().UTC()}
}

// Len devuelve cuántos nodos tiene el grafo — usado por diagnósticos y
// tests, nunca por lógica de negocio (el tamaño del grafo no debería
// condicionar ninguna decisión de este paquete).
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.nodes)
}
