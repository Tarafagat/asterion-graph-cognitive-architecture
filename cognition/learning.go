package cognition

import (
	"context"
	"sync"

	"github.com/Tarafagat/asterion-graph-cognitive-architecture/tool"
)

// Evaluator juzga una ejecución combinando fuentes independientes. La
// autoevaluación de la Tool entra como UNA señal más, nunca como la
// verdad final — una Tool no puede declarar unilateralmente que lo que
// hizo estuvo bien.
type Evaluator interface {
	Evaluate(ctx context.Context, d Decision, contract tool.Contract, result tool.ExecutionResult, worldBefore, worldAfter WorldSnapshot) Evaluation
}

// WorldSnapshot es una foto mínima del World alrededor de una ejecución
// — suficiente para juzgar si cambió como se esperaba, sin acoplar este
// paquete al Cognitive Graph entero.
type WorldSnapshot struct {
	Ref       string
	NodeCount int
}

// Weights son los pesos de la evaluación final. Configurables a
// propósito (§ 14): son política del operador, no una constante del
// runtime. Tool pesa MENOS que contrato/goal por diseño — es la única
// fuente que puede ser juez y parte.
type Weights struct {
	Tool     float64
	Contract float64
	Goal     float64
	World    float64
	User     float64
}

// DefaultWeights: el contrato y el goal dominan; la autoevaluación de la
// Tool aporta poco por sí sola; el usuario, cuando opina, pesa fuerte.
func DefaultWeights() Weights {
	return Weights{Tool: 0.15, Contract: 0.35, Goal: 0.30, World: 0.10, User: 0.10}
}

// StandardEvaluator es la implementación por defecto: mide contra el
// contrato declarado (guarantees), contra el intent y contra el cambio
// observado del World.
type StandardEvaluator struct {
	Weights Weights
}

// NewStandardEvaluator crea un evaluador con los pesos por defecto.
func NewStandardEvaluator() *StandardEvaluator {
	return &StandardEvaluator{Weights: DefaultWeights()}
}

// Evaluate calcula los cuatro scores independientes y su combinación.
// Una ejecución fallida nunca puede dar un FinalScore alto aunque la
// Tool se autoevalúe bien: el fallo domina ContractScore y GoalScore,
// que son los de mayor peso.
func (e *StandardEvaluator) Evaluate(
	_ context.Context,
	d Decision,
	contract tool.Contract,
	result tool.ExecutionResult,
	worldBefore, worldAfter WorldSnapshot,
) Evaluation {
	var ev Evaluation

	// 1. Tool: su autoevaluación si la dio; si no, el hecho crudo de si
	// falló o no. Acotada a [0,1] — una Tool no puede reportar 3.0 para
	// inflar su propia reputación.
	switch {
	case result.SelfEvaluation != nil:
		ev.ToolScore = clamp01(*result.SelfEvaluation)
	case result.Succeeded():
		ev.ToolScore = 1
	default:
		ev.ToolScore = 0
	}
	// Una ejecución con error nunca puede tener ToolScore alto, sin
	// importar lo que la Tool diga de sí misma.
	if !result.Succeeded() {
		ev.ToolScore = 0
	}

	// 2. Contrato: ¿cumplió lo que prometió devolver?
	if !result.Succeeded() {
		ev.ContractScore = 0
	} else if satisfied, checked := tool.GuaranteesSatisfied(contract, result.Output); checked == 0 {
		// Sin garantías verificables, no se premia ni se castiga: 0.5 es
		// "no hay evidencia", distinto de "cumplió" (1) o "falló" (0).
		ev.ContractScore = 0.5
	} else if satisfied {
		ev.ContractScore = 1
	} else {
		ev.ContractScore = 0
	}

	// 3. Goal: sin un verificador semántico real, la señal honesta es
	// "produjo salida para el intent pedido". 0.5 cuando no hay con qué
	// juzgarlo mejor — nunca 1 por defecto.
	switch {
	case !result.Succeeded():
		ev.GoalScore = 0
	case len(result.Output) > 0:
		ev.GoalScore = 1
	default:
		ev.GoalScore = 0.5
	}

	// 4. World: ¿cambió como corresponde al contrato? Una capability
	// mutante que no cambió NADA es sospechosa; una read_only que cambió
	// el World también.
	changed := worldAfter.NodeCount != worldBefore.NodeCount
	switch {
	case !result.Succeeded():
		ev.WorldScore = 0
	case contract.IsMutating() && !changed:
		ev.WorldScore = 0.25
	case contract.IsReadOnly() && changed:
		ev.WorldScore = 0.25
	default:
		ev.WorldScore = 1
	}

	w := e.Weights
	total := w.Tool + w.Contract + w.Goal + w.World
	sum := w.Tool*ev.ToolScore + w.Contract*ev.ContractScore + w.Goal*ev.GoalScore + w.World*ev.WorldScore
	if ev.UserScore != nil {
		sum += w.User * clamp01(*ev.UserScore)
		total += w.User
	}
	if total > 0 {
		ev.FinalScore = sum / total
	}
	_ = d // la Decision queda disponible para evaluadores más ricos
	return ev
}

// ConfidenceStore guarda la certeza aprendida por (capability, contexto)
// — nunca un único número global por capability.
type ConfidenceStore struct {
	mu     sync.RWMutex
	values map[string]map[string]float64 // capabilityID -> contextKey -> confidence
	counts map[string]map[string]int     // cuántas experiencias respaldan cada valor
}

// NewConfidenceStore crea un store vacío.
func NewConfidenceStore() *ConfidenceStore {
	return &ConfidenceStore{
		values: map[string]map[string]float64{},
		counts: map[string]map[string]int{},
	}
}

// PriorConfidence es la certeza de una capability nunca usada en un
// contexto: deliberadamente mediana-baja. No 0 (nunca se probaría nada
// nuevo) ni alta (se confiaría en algo sin evidencia).
const PriorConfidence = 0.5

// Get devuelve la certeza aprendida para una capability en un contexto,
// y cuántas experiencias la respaldan. Sin experiencias previas devuelve
// PriorConfidence con count 0 — el caller puede distinguir "no sé" de
// "aprendí que es 0.5".
func (s *ConfidenceStore) Get(capabilityID string, key ContextKey) (confidence float64, samples int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byCtx, ok := s.values[capabilityID]
	if !ok {
		return PriorConfidence, 0
	}
	v, ok := byCtx[key.String()]
	if !ok {
		return PriorConfidence, 0
	}
	return v, s.counts[capabilityID][key.String()]
}

// Update aplica una media móvil exponencial sobre la certeza de ESE
// contexto. alpha alto = aprende rápido y olvida rápido; bajo = más
// conservador. Solo toca la entrada de ese (capability, contexto): una
// experiencia en World=MedicalImage nunca mueve la certeza de
// World=FinancialAnalysis.
func (s *ConfidenceStore) Update(capabilityID string, key ContextKey, observed, alpha float64) (previous, updated float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctxKey := key.String()
	if s.values[capabilityID] == nil {
		s.values[capabilityID] = map[string]float64{}
		s.counts[capabilityID] = map[string]int{}
	}
	previous, seen := s.values[capabilityID][ctxKey]
	if !seen {
		previous = PriorConfidence
	}
	updated = clamp01((1-alpha)*previous + alpha*clamp01(observed))
	s.values[capabilityID][ctxKey] = updated
	s.counts[capabilityID][ctxKey]++
	return previous, updated
}

// Snapshot devuelve una copia de todo lo aprendido — para persistir o
// inspeccionar.
func (s *ConfidenceStore) Snapshot() map[string]map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]map[string]float64, len(s.values))
	for cap, byCtx := range s.values {
		inner := make(map[string]float64, len(byCtx))
		for k, v := range byCtx {
			inner[k] = v
		}
		out[cap] = inner
	}
	return out
}

// Restore carga un snapshot previo (persistencia entre corridas).
func (s *ConfidenceStore) Restore(data map[string]map[string]float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for cap, byCtx := range data {
		if s.values[cap] == nil {
			s.values[cap] = map[string]float64{}
			s.counts[cap] = map[string]int{}
		}
		for k, v := range byCtx {
			s.values[cap][k] = v
		}
	}
}

// Learner convierte una Evaluation en un cambio de certeza. Interfaz a
// propósito: hoy es una media móvil, mañana puede ser bayesiano o un
// modelo entrenado, sin tocar el resto del runtime.
type Learner interface {
	Learn(ctx context.Context, store *ConfidenceStore, capabilityID string, key ContextKey, ev Evaluation) (previous, updated float64)
}

// EMALearner es el aprendizaje por defecto: media móvil exponencial
// sobre el FinalScore de la evaluación.
type EMALearner struct {
	Alpha float64
}

// NewEMALearner crea un learner con un alpha moderado — aprende de cada
// experiencia sin que una sola borre todo lo anterior.
func NewEMALearner() *EMALearner { return &EMALearner{Alpha: 0.3} }

func (l *EMALearner) Learn(_ context.Context, store *ConfidenceStore, capabilityID string, key ContextKey, ev Evaluation) (float64, float64) {
	return store.Update(capabilityID, key, ev.FinalScore, l.Alpha)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
