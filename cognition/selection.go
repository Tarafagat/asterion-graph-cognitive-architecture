package cognition

import (
	"sort"
	"strings"

	"github.com/Tarafagat/asterion-graph-cognitive-architecture/tool"
)

// ScoreWeights son los coeficientes de la selección cognitiva (§ 16):
//
//	Score = α*GoalSimilarity + β*WorldSimilarity + γ*ContractCompatibility
//	        + δ*ExperienceConfidence − ε*PolicyPenalty
type ScoreWeights struct {
	Goal       float64 // α
	World      float64 // β
	Contract   float64 // γ
	Experience float64 // δ
	Policy     float64 // ε
}

// DefaultScoreWeights: la experiencia acumulada pesa tanto como el
// encaje declarativo con el goal — así el aprendizaje cambia decisiones
// de verdad, que es el punto del Segundo Principio.
func DefaultScoreWeights() ScoreWeights {
	return ScoreWeights{Goal: 0.30, World: 0.15, Contract: 0.20, Experience: 0.35, Policy: 1.0}
}

// MinimumConfidence es el piso por debajo del cual NO se ejecuta nada.
// AGCA prefiere no actuar antes que actuar sin certeza suficiente — y
// nunca inventa una capability que no exista para salir del paso.
const MinimumConfidence = 0.35

// PolicyGate decide si una capability está permitida ANTES de ejecutar.
// Devuelve allowed + motivo (el motivo se guarda aunque esté permitida,
// para la traza).
type PolicyGate interface {
	Check(contract tool.Contract, intent Intent) (allowed bool, policy string, reason string)
}

// PermissionGate es la frontera de autoridad del AGENTE (allow/deny de
// AGCA.agent) — separada de PolicyGate a propósito: una política es del
// sistema, un permiso es de quién actúa.
type PermissionGate interface {
	Permitted(capabilityID string) (allowed bool, reason string)
}

// Selector arma una Decision completa: puntúa todos los contratos
// registrados, aplica políticas y permisos, y elige el mejor por encima
// del umbral — o ninguno, con su motivo.
type Selector struct {
	Weights    ScoreWeights
	Minimum    float64
	Confidence *ConfidenceStore
}

// NewSelector crea un selector con los pesos y el umbral por defecto.
func NewSelector(confidence *ConfidenceStore) *Selector {
	return &Selector{Weights: DefaultScoreWeights(), Minimum: MinimumConfidence, Confidence: confidence}
}

// Select evalúa todos los contratos disponibles y devuelve la Decision
// (todavía sin ejecutar). Nunca devuelve error: "no hay nada ejecutable"
// es una Decision válida con NoExecutionReason — un resultado cognitivo,
// no una falla del sistema.
func (s *Selector) Select(
	contracts []tool.Contract,
	intent Intent,
	key ContextKey,
	goalVec, worldVec Vector,
	policies PolicyGate,
	permissions PermissionGate,
) Decision {
	d := Decision{
		WorldID:           key.World,
		Intent:            intent,
		ObservationVector: goalVec,
		CreatedAt:         nowUTC(),
	}
	d.ReasoningTrace.Add("intent=" + intent.Name + " goal=" + intent.Goal)
	d.ReasoningTrace.Add("context=" + key.String())

	if len(contracts) == 0 {
		d.NoExecutionReason = "no hay ninguna capability de Tool registrada"
		d.ReasoningTrace.Add("sin candidatos: el Capability Registry está vacío")
		return d
	}

	for _, c := range contracts {
		cand := Candidate{CapabilityID: c.ID}

		capVec := ContractVector(c)
		cand.GoalSimilarity = capVec.Similarity(goalVec)
		cand.WorldSimilarity = capVec.Similarity(worldVec)
		cand.ContractCompat = contractCompatibility(c, intent)
		cand.ExperienceConfidence, _ = s.Confidence.Get(c.ID, key)

		// Permisos del agente primero: es la frontera más dura (deny
		// gana sobre cualquier score alto).
		if permissions != nil {
			if allowed, reason := permissions.Permitted(c.ID); !allowed {
				cand.Rejected, cand.RejectedReason = true, reason
				cand.PolicyPenalty = 1
			}
		}
		// Políticas del sistema después.
		if !cand.Rejected && policies != nil {
			allowed, policy, reason := policies.Check(c, intent)
			// Solo se registra la política que DECIDIÓ algo: una fila
			// "permitida por nadie en particular" no explica nada y
			// ensucia la traza de la Decision.
			if policy != "" {
				d.PoliciesApplied = append(d.PoliciesApplied, PolicyDecision{
					Policy: policy, CapabilityID: c.ID, Allowed: allowed, Reason: reason,
				})
			}
			if !allowed {
				cand.Rejected, cand.RejectedReason = true, reason
				cand.PolicyPenalty = 1
			}
		}

		w := s.Weights
		cand.Score = w.Goal*cand.GoalSimilarity +
			w.World*cand.WorldSimilarity +
			w.Contract*cand.ContractCompat +
			w.Experience*cand.ExperienceConfidence -
			w.Policy*cand.PolicyPenalty
		if cand.Score < 0 {
			cand.Score = 0
		}
		d.CandidateCapabilities = append(d.CandidateCapabilities, cand)
	}

	sort.SliceStable(d.CandidateCapabilities, func(i, j int) bool {
		return d.CandidateCapabilities[i].Score > d.CandidateCapabilities[j].Score
	})

	var best *Candidate
	for i := range d.CandidateCapabilities {
		c := &d.CandidateCapabilities[i]
		if c.Rejected {
			d.ReasoningTrace.Add("descartada " + c.CapabilityID + ": " + c.RejectedReason)
			continue
		}
		if best == nil {
			best = c
		}
	}

	if best == nil {
		d.NoExecutionReason = "todas las capabilities candidatas fueron descartadas por políticas o permisos"
		d.ReasoningTrace.Add("NO_EXECUTION: " + d.NoExecutionReason)
		return d
	}
	if best.Score < s.Minimum {
		d.NoExecutionReason = "ninguna capability alcanzó la certeza mínima requerida"
		d.Confidence = best.Score
		d.ReasoningTrace.Add("NO_EXECUTION: mejor candidata " + best.CapabilityID + " no llegó al umbral")
		return d
	}

	d.SelectedCapability = best.CapabilityID
	d.Confidence = best.Score
	d.ContractRef = best.CapabilityID
	d.ReasoningTrace.Add("seleccionada " + best.CapabilityID + " por score compuesto sobre el umbral")
	return d
}

// ContractVector traduce un contrato declarado a un vector explícito —
// sin embeddings, solo lo que el contrato YA dice de sí mismo (§ 5 y
// § 20 del pedido de Experience).
func ContractVector(c tool.Contract) Vector {
	v := Vector{}
	if c.Category != "" {
		v["domain."+c.Category] = 1
	}
	if c.Tool != "" {
		v["tool."+strings.ToLower(c.Tool)] = 0.5
	}
	// La operación sale del nombre de la capability: "search_series" ->
	// operation.search + operation.series.
	for _, part := range strings.Split(c.Name, "_") {
		if part != "" {
			v["operation."+part] = 1
		}
	}
	for _, e := range c.Effects {
		v[normalizeFeature(e)] = 1
	}
	if c.IsMutating() {
		v["mutating"] = 1
	}
	if c.IsReadOnly() {
		v["read_only"] = 1
	}
	if len(c.Requires) > 0 {
		v["external_access"] = 0.7
	}
	return v
}

// GoalVector traduce un goal en texto a features explícitas: cada
// palabra significativa es una feature "operation.<palabra>", que es lo
// que la hace comparable con ContractVector sin ningún modelo entrenado.
//
// Es LÉXICO a propósito, no semántico: un goal en castellano no se va a
// parecer a una capability nombrada en inglés, y eso queda visible como
// goal=0.00 en la Decision en vez de disimularse. Para eso está
// ContractCompatibility (que compara contra el intent declarado) y, sobre
// todo, la experiencia acumulada — que es la señal que de verdad aprende.
func GoalVector(goal string) Vector {
	return goalVectorFrom(goal)
}

// IntentVector combina el texto del goal con el nombre del intent: un
// intent "search_series" describe la operación buscada tan bien como el
// texto libre, y sus tokens SÍ son comparables con los de una capability.
func IntentVector(intent Intent) Vector {
	v := goalVectorFrom(intent.Goal)
	for _, part := range strings.Split(strings.ToLower(intent.Name), "_") {
		if len(part) >= 3 {
			v["operation."+part] = 1
		}
	}
	return v
}

func goalVectorFrom(goal string) Vector {
	v := Vector{}
	for _, w := range strings.Fields(strings.ToLower(goal)) {
		w = strings.Trim(w, ".,;:¿?¡!()\"'")
		if len(w) < 3 {
			continue
		}
		v["operation."+w] = 1
	}
	return v
}

// contractCompatibility mide cuán compatible es el contrato con el
// intent declarado, hoy por coincidencia de nombre — una señal honesta y
// verificable, no una inferencia semántica que este runtime no puede
// respaldar todavía.
func contractCompatibility(c tool.Contract, intent Intent) float64 {
	name := strings.ToLower(intent.Name)
	if name == "" {
		return 0.5
	}
	capName := strings.ReplaceAll(strings.ToLower(c.Name), "_", "")
	if strings.Contains(strings.ReplaceAll(name, "_", ""), capName) {
		return 1
	}
	var hits int
	parts := strings.Split(strings.ToLower(c.Name), "_")
	for _, p := range parts {
		if p != "" && strings.Contains(name, p) {
			hits++
		}
	}
	if len(parts) == 0 {
		return 0.5
	}
	return float64(hits) / float64(len(parts))
}

func normalizeFeature(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), " ", "_")
}
