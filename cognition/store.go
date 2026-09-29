package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DecisionStore y ExperienceStore son interfaces a propósito (§ 19): la
// lógica cognitiva nunca se acopla a JSON ni a un motor puntual. Hoy hay
// una implementación en memoria y una en archivo; mañana puede haber
// SQLite o algo vectorial sin tocar el runtime.
type DecisionStore interface {
	Save(ctx context.Context, d Decision) error
	Get(ctx context.Context, id string) (Decision, error)
	List(ctx context.Context, limit int) ([]Decision, error)
}

type ExperienceStore interface {
	Save(ctx context.Context, e Experience) error
	Get(ctx context.Context, id string) (Experience, error)
	List(ctx context.Context, limit int) ([]Experience, error)
	// FindSimilar busca experiencias de contextos PARECIDOS para una
	// capability — la base de "estados futuros semejantes del World" del
	// Segundo Principio.
	FindSimilar(ctx context.Context, capabilityID string, contextVec Vector, limit int) ([]Experience, error)
}

// --- En memoria ----------------------------------------------------------

type memoryStore struct {
	mu          sync.RWMutex
	decisions   []Decision
	experiences []Experience
}

// NewMemoryStore crea un store en memoria que implementa las dos
// interfaces — útil para tests y para una corrida que no quiera
// persistir nada.
func NewMemoryStore() *memoryStore { return &memoryStore{} }

func (m *memoryStore) Save(_ context.Context, d Decision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decisions = append(m.decisions, d)
	return nil
}

func (m *memoryStore) Get(_ context.Context, id string) (Decision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, d := range m.decisions {
		if d.ID == id {
			return d, nil
		}
	}
	return Decision{}, fmt.Errorf("cognition: no existe la decision %q", id)
}

func (m *memoryStore) List(_ context.Context, limit int) ([]Decision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return lastN(m.decisions, limit), nil
}

func (m *memoryStore) SaveExperience(_ context.Context, e Experience) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.experiences = append(m.experiences, e)
	return nil
}

func (m *memoryStore) Experiences() []Experience {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Experience(nil), m.experiences...)
}

// --- Persistente en archivo ----------------------------------------------

// FileStore persiste decisiones, experiencias y la certeza aprendida en
// JSON bajo un directorio propio — lo mínimo para que AGCA RECUERDE
// entre invocaciones del CLI (sin esto, cada `asterion graph run` sería
// una corrida amnésica y el Segundo Principio no podría cumplirse).
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore crea (si hace falta) el directorio y devuelve el store.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cognition: no pude crear %s: %w", dir, err)
	}
	return &FileStore{dir: dir}, nil
}

// Dir devuelve el directorio donde persiste.
func (f *FileStore) Dir() string { return f.dir }

func (f *FileStore) decisionsPath() string  { return filepath.Join(f.dir, "decisions.json") }
func (f *FileStore) experiencePath() string { return filepath.Join(f.dir, "experiences.json") }
func (f *FileStore) confidencePath() string { return filepath.Join(f.dir, "confidence.json") }

func (f *FileStore) Save(_ context.Context, d Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []Decision
	if err := readJSON(f.decisionsPath(), &all); err != nil {
		return err
	}
	all = append(all, d)
	return writeJSON(f.decisionsPath(), all)
}

func (f *FileStore) Get(_ context.Context, id string) (Decision, error) {
	var all []Decision
	if err := readJSON(f.decisionsPath(), &all); err != nil {
		return Decision{}, err
	}
	for _, d := range all {
		if d.ID == id {
			return d, nil
		}
	}
	return Decision{}, fmt.Errorf("cognition: no existe la decision %q", id)
}

func (f *FileStore) List(_ context.Context, limit int) ([]Decision, error) {
	var all []Decision
	if err := readJSON(f.decisionsPath(), &all); err != nil {
		return nil, err
	}
	return lastN(all, limit), nil
}

func (f *FileStore) SaveExperience(_ context.Context, e Experience) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []Experience
	if err := readJSON(f.experiencePath(), &all); err != nil {
		return err
	}
	all = append(all, e)
	return writeJSON(f.experiencePath(), all)
}

func (f *FileStore) GetExperience(_ context.Context, id string) (Experience, error) {
	var all []Experience
	if err := readJSON(f.experiencePath(), &all); err != nil {
		return Experience{}, err
	}
	for _, e := range all {
		if e.ID == id {
			return e, nil
		}
	}
	return Experience{}, fmt.Errorf("cognition: no existe la experience %q", id)
}

func (f *FileStore) ListExperiences(_ context.Context, limit int) ([]Experience, error) {
	var all []Experience
	if err := readJSON(f.experiencePath(), &all); err != nil {
		return nil, err
	}
	return lastN(all, limit), nil
}

// FindSimilar devuelve las experiencias de esa capability ordenadas por
// similitud de contexto (mayor primero) — la evidencia que respalda una
// certeza en un contexto parecido.
func (f *FileStore) FindSimilar(_ context.Context, capabilityID string, contextVec Vector, limit int) ([]Experience, error) {
	var all []Experience
	if err := readJSON(f.experiencePath(), &all); err != nil {
		return nil, err
	}
	type scored struct {
		exp Experience
		sim float64
	}
	var matches []scored
	for _, e := range all {
		if e.SelectedCapability != capabilityID {
			continue
		}
		matches = append(matches, scored{e, e.ContextVector.Similarity(contextVec)})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].sim > matches[j].sim })
	out := make([]Experience, 0, len(matches))
	for i, m := range matches {
		if limit > 0 && i >= limit {
			break
		}
		out = append(out, m.exp)
	}
	return out, nil
}

// SaveConfidence/LoadConfidence persisten lo aprendido — es lo que hace
// que la certeza sobreviva entre corridas del CLI.
func (f *FileStore) SaveConfidence(s *ConfidenceStore) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return writeJSON(f.confidencePath(), s.Snapshot())
}

func (f *FileStore) LoadConfidence(s *ConfidenceStore) error {
	var data map[string]map[string]float64
	if err := readJSON(f.confidencePath(), &data); err != nil {
		return err
	}
	if data != nil {
		s.Restore(data)
	}
	return nil
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // todavía no hay nada guardado: no es un error
		}
		return fmt.Errorf("cognition: no pude leer %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("cognition: %s no es JSON válido: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func lastN[T any](all []T, limit int) []T {
	if limit <= 0 || limit >= len(all) {
		return append([]T(nil), all...)
	}
	return append([]T(nil), all[len(all)-limit:]...)
}

func nowUTC() time.Time { return time.Now().UTC() }
