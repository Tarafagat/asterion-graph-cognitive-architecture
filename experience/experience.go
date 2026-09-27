// Package experience implementa el Experience Graph del paper (§ 9.5):
// cada ciclo cognitivo registra qué se pidió, qué se usó y qué resultó,
// para que el routing futuro pueda aprender de corridas pasadas
// (Milestone 8 del roadmap: "Event Ingestion + ExperienceRecord"; MVP 5:
// "modificar el routing futuro basándose en métricas observadas"). Este
// paquete implementa el registro — LEER esas métricas para influir
// routing.learn_from (Apéndice C) es Fase 6 del roadmap, deliberadamente
// no implementado acá todavía: un Store que solo acumula sin que nada lo
// lea todavía es honesto; fingir un algoritmo de aprendizaje sin datos
// reales para entrenarlo no lo sería.
package experience

import (
	"sync"
	"time"
)

// Record es una fila del Experience Graph — los campos que el paper pide
// en § 9.5 que este MVP ya puede llenar de verdad con lo que
// runtime.CognitiveCycle produce (context/subgrafo consultado/decisiones
// intermedias quedan para cuando el planner sea más que "una capability
// fija por CLI flag", ver runtime/runtime.go).
type Record struct {
	TraceID    string
	Goal       string
	NeuronUsed string
	Result     string
	Err        string
	StartedAt  time.Time
	Duration   time.Duration
}

// Store acumula Records en memoria, seguro para uso concurrente.
type Store struct {
	mu      sync.RWMutex
	records []Record
}

// NewStore crea un Experience Store vacío.
func NewStore() *Store {
	return &Store{}
}

// Record agrega una experiencia.
func (s *Store) Record(r Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
}

// All devuelve todas las experiencias registradas, en orden cronológico
// de inserción — una copia, para que el caller no pueda mutar el store
// por accidente.
func (s *Store) All() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}

// Len devuelve cuántas experiencias hay registradas.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}
