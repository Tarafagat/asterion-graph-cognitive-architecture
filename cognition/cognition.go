// Package cognition implementa el Segundo Principio de AGI de AGCA:
//
//	AGCA aprende cuando los resultados de sus interacciones con un World
//	modifican la certeza con la que seleccionará capacidades frente a
//	estados futuros semejantes del World.
//
// Las tres piezas y su relación:
//
//	World(t0) -> Observation -> Intent -> Decision -> Capability Selection
//	  -> Tool Execution -> World(t1) -> Evaluation -> Experience
//	  -> Confidence Update -> Future Decision
//
// Una Decision registra POR QUÉ se eligió una capability (candidatos,
// scores, políticas que descartaron, certeza previa) y es inspeccionable
// después — aprender nunca puede costar trazabilidad. Una Experience
// registra QUÉ PASÓ de verdad al ejecutarla, evaluado por varias fuentes
// independientes, y de ahí sale el delta de certeza.
//
// Los vectores de este paquete son explícitos y declarativos (features
// del contrato, del World y del goal) — NO dependen de embeddings de
// Transformers. Un Transformer puede ser una neurona más que produzca un
// vector, pero nunca un requisito arquitectónico (§ 20 del pedido de
// Experience).
package cognition

import (
	"math"
	"sort"
	"time"
)

// Vector es una representación explícita y nombrada — no un embedding
// opaco. Cada clave es una feature declarativa ("domain.statistics",
// "operation.search", "read_only") con su peso. Dos vectores se comparan
// por similitud coseno sobre las claves que comparten.
type Vector map[string]float64

// Similarity es la similitud coseno entre dos vectores, en [0,1] para
// vectores de componentes no negativas. Dos vectores sin ninguna clave en
// común dan 0 — "no se parecen en nada", que es lo que corresponde, no un
// valor neutro.
func (v Vector) Similarity(other Vector) float64 {
	if len(v) == 0 || len(other) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for k, a := range v {
		normA += a * a
		if b, ok := other[k]; ok {
			dot += a * b
		}
	}
	for _, b := range other {
		normB += b * b
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// Keys devuelve las claves ordenadas — para salidas deterministas.
func (v Vector) Keys() []string {
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Intent es lo que la Intelligence intenta resolver — el goal
// interpretado, no el texto crudo. Goal conserva el texto original para
// trazabilidad.
type Intent struct {
	Name string // ej. "SearchStatisticalDataset"
	Goal string // el texto original del pedido
	// RequiredCapability es la capacidad de NEURONA que el ciclo pide
	// (ej. "classification") — distinta de las capabilities de Tool, que
	// son acciones sobre el World.
	RequiredCapability string
}

// ContextKey identifica el CONTEXTO en el que se toma una decisión. La
// certeza nunca es global por capability: "statistics.search_series" puede
// ser excelente en World=FinancialAnalysis/Goal=HistoricalPriceSearch y
// pésima en World=MedicalImage/Goal=DetectTumor (§ 15 del pedido de
// Experience). Esta clave es lo que evita que experiencias de contextos
// distintos se contaminen entre sí.
type ContextKey struct {
	World string
	Goal  string
}

func (c ContextKey) String() string { return c.World + "|" + c.Goal }

// Candidate es una capability considerada para una Decision, con el
// desglose COMPLETO de su score — no solo el número final: poder
// reconstruir por qué ganó una y no otra es parte del contrato de
// trazabilidad.
type Candidate struct {
	CapabilityID string  `json:"capability_id"`
	Score        float64 `json:"score"`

	GoalSimilarity       float64 `json:"goal_similarity"`
	WorldSimilarity      float64 `json:"world_similarity"`
	ContractCompat       float64 `json:"contract_compatibility"`
	ExperienceConfidence float64 `json:"experience_confidence"`
	PolicyPenalty        float64 `json:"policy_penalty"`

	// Rejected/RejectedReason: un candidato descartado NO desaparece de la
	// Decision — queda con el motivo exacto (política, permiso del agente,
	// requirement incumplido), que es lo que hace explicable la decisión.
	Rejected       bool   `json:"rejected"`
	RejectedReason string `json:"rejected_reason,omitempty"`
}

// PolicyDecision registra qué política se evaluó sobre qué candidato y
// con qué resultado.
type PolicyDecision struct {
	Policy       string `json:"policy"`
	CapabilityID string `json:"capability_id"`
	Allowed      bool   `json:"allowed"`
	Reason       string `json:"reason,omitempty"`
}

// DecisionTrace es el rastro legible de cómo se llegó a la selección —
// pasos en orden, para responder "¿por qué ejecutaste esto?".
type DecisionTrace struct {
	Steps []string `json:"steps"`
}

func (t *DecisionTrace) Add(step string) { t.Steps = append(t.Steps, step) }

// Decision es una selección cognitiva completa y auditable. Existe
// ANTES de ejecutar: registra qué se iba a hacer y por qué, incluso si
// después la ejecución falla o si no se ejecuta nada
// (SelectedCapability vacío + NoExecutionReason).
type Decision struct {
	ID string `json:"id"`

	WorldID       string `json:"world_id"`
	WorldStateRef string `json:"world_state_ref"`

	Intent            Intent `json:"intent"`
	ObservationVector Vector `json:"observation_vector"`

	CandidateCapabilities []Candidate      `json:"candidate_capabilities"`
	PoliciesApplied       []PolicyDecision `json:"policies_applied"`

	SelectedCapability string  `json:"selected_capability"`
	Confidence         float64 `json:"confidence"`
	ContractRef        string  `json:"contract_ref,omitempty"`

	// Role/User: EN NOMBRE DE QUIÉN se decidió. Queda en la Decision
	// porque "qué podía hacer quien pidió esto" es parte de por qué se
	// eligió lo que se eligió — y de por qué se descartó el resto.
	Role string `json:"role,omitempty"`
	User string `json:"user,omitempty"`

	// NoExecutionReason explica por qué NO se seleccionó nada — AGCA debe
	// poder rechazar una acción por falta de certeza (§ 16), y eso es un
	// resultado legítimo que también se registra, nunca un silencio.
	NoExecutionReason string `json:"no_execution_reason,omitempty"`

	ReasoningTrace DecisionTrace `json:"reasoning_trace"`
	CreatedAt      time.Time     `json:"created_at"`
}

// Executed responde si la Decision terminó seleccionando algo.
func (d Decision) Executed() bool { return d.SelectedCapability != "" }

// Evaluation es el juicio sobre una ejecución, combinando fuentes
// INDEPENDIENTES. La autoevaluación de la Tool es solo una de ellas y
// nunca decide sola (§ 13 del pedido de Experience).
type Evaluation struct {
	ToolScore     float64 `json:"tool_score"`     // autoevaluación de la Tool (o derivada del éxito/error)
	ContractScore float64 `json:"contract_score"` // ¿cumplió las guarantees declaradas?
	GoalScore     float64 `json:"goal_score"`     // ¿sirvió para el intent?
	WorldScore    float64 `json:"world_score"`    // ¿el World cambió como se esperaba?

	UserScore *float64 `json:"user_score,omitempty"` // opcional, humano

	FinalScore float64 `json:"final_score"`
}

// Experience es el resultado observable de una Decision — evidencia
// adquirida por interacción, no memoria cronológica:
//
//	Experience = WorldBefore + Decision + Execution + WorldAfter
//	             + Evaluation + ConfidenceDelta
type Experience struct {
	ID         string `json:"id"`
	DecisionID string `json:"decision_id"`

	WorldBeforeRef string `json:"world_before_ref"`
	WorldAfterRef  string `json:"world_after_ref"`

	SelectedCapability string `json:"selected_capability"`

	Succeeded    bool   `json:"succeeded"`
	ErrorMessage string `json:"error_message,omitempty"`
	DurationMS   int64  `json:"duration_ms"`

	Evaluation Evaluation `json:"evaluation"`

	Context            ContextKey `json:"context"`
	ContextVector      Vector     `json:"context_vector"`
	PreviousConfidence float64    `json:"previous_confidence"`
	ResultConfidence   float64    `json:"result_confidence"`
	ConfidenceDelta    float64    `json:"confidence_delta"`

	CreatedAt time.Time `json:"created_at"`
}
