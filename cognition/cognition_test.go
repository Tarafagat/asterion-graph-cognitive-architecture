package cognition

import (
	"context"
	"testing"

	"github.com/Tarafagat/asterion-graph-cognitive-architecture/tool"
)

func TestVector_Similarity(t *testing.T) {
	a := Vector{"domain.statistics": 1, "operation.search": 1}
	if got := a.Similarity(a); got < 0.999 {
		t.Errorf("Similarity consigo mismo = %v, want ~1", got)
	}
	b := Vector{"domain.medicine": 1, "operation.detect": 1}
	if got := a.Similarity(b); got != 0 {
		t.Errorf("Similarity sin claves en común = %v, want 0", got)
	}
	if got := a.Similarity(Vector{}); got != 0 {
		t.Errorf("Similarity con vector vacío = %v, want 0", got)
	}
}

// El núcleo del Segundo Principio: una experiencia cambia la certeza del
// contexto en el que ocurrió — y SOLO de ese contexto.
func TestConfidenceStore_ContextsDoNotContaminate(t *testing.T) {
	s := NewConfidenceStore()
	financial := ContextKey{World: "FinancialAnalysis", Goal: "HistoricalPriceSearch"}
	medical := ContextKey{World: "MedicalImage", Goal: "DetectTumor"}

	if c, samples := s.Get("statistics.search_series", financial); c != PriorConfidence || samples != 0 {
		t.Fatalf("sin experiencias = (%v,%d), want (%v,0)", c, samples, PriorConfidence)
	}

	// Varias experiencias buenas en el contexto financiero.
	for i := 0; i < 5; i++ {
		s.Update("statistics.search_series", financial, 1.0, 0.3)
	}
	fin, finSamples := s.Get("statistics.search_series", financial)
	if fin <= PriorConfidence {
		t.Errorf("la certeza financiera = %v, debería haber subido sobre %v", fin, PriorConfidence)
	}
	if finSamples != 5 {
		t.Errorf("samples = %d, want 5", finSamples)
	}

	// El contexto médico NO se contaminó.
	med, medSamples := s.Get("statistics.search_series", medical)
	if med != PriorConfidence || medSamples != 0 {
		t.Errorf("contexto médico = (%v,%d), want (%v,0) — las experiencias de otro World no deben contaminarlo", med, medSamples, PriorConfidence)
	}

	// Y una mala experiencia médica baja SOLO la médica.
	for i := 0; i < 5; i++ {
		s.Update("statistics.search_series", medical, 0.0, 0.3)
	}
	if med, _ := s.Get("statistics.search_series", medical); med >= PriorConfidence {
		t.Errorf("certeza médica = %v, debería haber bajado", med)
	}
	if after, _ := s.Get("statistics.search_series", financial); after != fin {
		t.Errorf("la certeza financiera cambió (%v -> %v) por experiencias médicas", fin, after)
	}
}

func TestConfidenceStore_SnapshotRestore(t *testing.T) {
	s := NewConfidenceStore()
	key := ContextKey{World: "W", Goal: "G"}
	s.Update("a.b", key, 1.0, 0.5)
	snap := s.Snapshot()

	restored := NewConfidenceStore()
	restored.Restore(snap)
	got, _ := restored.Get("a.b", key)
	want, _ := s.Get("a.b", key)
	if got != want {
		t.Errorf("restaurado = %v, want %v", got, want)
	}
}

// La autoevaluación de la Tool no puede fijar por sí sola la confianza
// final: si el contrato no se cumplió, el score final tiene que caer.
func TestEvaluator_ToolSelfEvaluationIsNotTheFinalTruth(t *testing.T) {
	e := NewStandardEvaluator()
	contract := tool.Contract{ID: "s.search", Effects: []string{tool.EffectReadOnly}, Guarantees: []string{"returns:series"}}
	before := WorldSnapshot{Ref: "w@1", NodeCount: 1}
	after := WorldSnapshot{Ref: "w@1", NodeCount: 1}

	perfect := 1.0
	// La Tool se autoevalúa perfecta, pero NO devolvió lo que su contrato
	// garantizaba.
	lying := tool.ExecutionResult{Output: tool.Output{"otra_cosa": 1}, SelfEvaluation: &perfect}
	ev := e.Evaluate(context.Background(), Decision{}, contract, lying, before, after)

	if ev.ToolScore != 1 {
		t.Errorf("ToolScore = %v, want 1 (es lo que la Tool dijo de sí misma)", ev.ToolScore)
	}
	if ev.ContractScore != 0 {
		t.Errorf("ContractScore = %v, want 0 (no cumplió returns:series)", ev.ContractScore)
	}
	if ev.FinalScore >= 0.7 {
		t.Errorf("FinalScore = %v — una Tool no puede declararse exitosa sola cuando incumplió su contrato", ev.FinalScore)
	}

	// Cumpliendo el contrato, el score final sí sube.
	honest := tool.ExecutionResult{Output: tool.Output{"series": 1}, SelfEvaluation: &perfect}
	evOK := e.Evaluate(context.Background(), Decision{}, contract, honest, before, after)
	if evOK.FinalScore <= ev.FinalScore {
		t.Errorf("cumplir el contrato debería dar mejor score: %v vs %v", evOK.FinalScore, ev.FinalScore)
	}
}

func TestEvaluator_FailedExecutionScoresZeroDespiteSelfEvaluation(t *testing.T) {
	e := NewStandardEvaluator()
	perfect := 1.0
	failed := tool.ExecutionResult{Err: context.Canceled, SelfEvaluation: &perfect}
	ev := e.Evaluate(context.Background(), Decision{}, tool.Contract{ID: "x"}, failed,
		WorldSnapshot{NodeCount: 1}, WorldSnapshot{NodeCount: 1})

	if ev.ToolScore != 0 || ev.ContractScore != 0 || ev.GoalScore != 0 {
		t.Errorf("una ejecución fallida no puede puntuar bien: %+v", ev)
	}
	if ev.FinalScore != 0 {
		t.Errorf("FinalScore = %v, want 0", ev.FinalScore)
	}
}

func TestEvaluator_ReadOnlyThatChangedWorldIsPenalized(t *testing.T) {
	e := NewStandardEvaluator()
	ro := tool.Contract{ID: "s.search", Effects: []string{tool.EffectReadOnly}}
	result := tool.ExecutionResult{Output: tool.Output{"x": 1}}

	quiet := e.Evaluate(context.Background(), Decision{}, ro, result, WorldSnapshot{NodeCount: 2}, WorldSnapshot{NodeCount: 2})
	noisy := e.Evaluate(context.Background(), Decision{}, ro, result, WorldSnapshot{NodeCount: 2}, WorldSnapshot{NodeCount: 9})
	if noisy.WorldScore >= quiet.WorldScore {
		t.Errorf("una capability read_only que cambió el World debería penalizarse: %v vs %v", noisy.WorldScore, quiet.WorldScore)
	}
}

func TestLearner_UpdatesConfidence(t *testing.T) {
	store := NewConfidenceStore()
	l := NewEMALearner()
	key := ContextKey{World: "W", Goal: "G"}

	prev, updated := l.Learn(context.Background(), store, "a.b", key, Evaluation{FinalScore: 1})
	if prev != PriorConfidence {
		t.Errorf("previous = %v, want %v", prev, PriorConfidence)
	}
	if updated <= prev {
		t.Errorf("una evaluación perfecta debería subir la certeza: %v -> %v", prev, updated)
	}
}

func contracts() []tool.Contract {
	return []tool.Contract{
		{ID: "statistics.search_series", Tool: "Statistics", Name: "search_series", Category: "statistics", Effects: []string{tool.EffectReadOnly}},
		{ID: "database.raw_sql", Tool: "Database", Name: "raw_sql", Category: "database", Effects: []string{tool.EffectDestructive}},
	}
}

type denyAll struct{}

func (denyAll) Permitted(id string) (bool, string) { return false, "no autorizado: " + id }

type allowList map[string]bool

func (a allowList) Permitted(id string) (bool, string) {
	if a[id] {
		return true, ""
	}
	return false, "el agente no tiene " + id + " en su allow"
}

// Una Decision debe contener SUS candidatos con scores — incluidos los
// rechazados, con el motivo.
func TestSelector_DecisionRecordsCandidatesAndRejections(t *testing.T) {
	s := NewSelector(NewConfidenceStore())
	intent := Intent{Name: "search_series", Goal: "buscar la serie histórica"}
	key := ContextKey{World: "EconomicResearch", Goal: intent.Name}

	d := s.Select(contracts(), intent, key, GoalVector(intent.Goal), Vector{"world.economicresearch": 1},
		nil, allowList{"statistics.search_series": true})

	if len(d.CandidateCapabilities) != 2 {
		t.Fatalf("candidatos = %d, want 2 (todos los evaluados quedan registrados)", len(d.CandidateCapabilities))
	}
	if d.SelectedCapability != "statistics.search_series" {
		t.Fatalf("seleccionada = %q, want statistics.search_series", d.SelectedCapability)
	}

	var sawRejected bool
	for _, c := range d.CandidateCapabilities {
		if c.CapabilityID == "database.raw_sql" {
			sawRejected = c.Rejected && c.RejectedReason != ""
		}
		if c.Score < 0 {
			t.Errorf("score negativo en %s: %v", c.CapabilityID, c.Score)
		}
	}
	if !sawRejected {
		t.Error("database.raw_sql debería figurar como rechazada con su motivo, no desaparecer")
	}
	if len(d.ReasoningTrace.Steps) == 0 {
		t.Error("la Decision debe traer su traza de razonamiento")
	}
}

// Sin permisos, no se ejecuta NADA — deny-by-default.
func TestSelector_NoExecutionWhenEverythingDenied(t *testing.T) {
	s := NewSelector(NewConfidenceStore())
	d := s.Select(contracts(), Intent{Name: "search_series"}, ContextKey{World: "W", Goal: "search_series"},
		GoalVector("buscar serie"), Vector{}, nil, denyAll{})

	if d.Executed() {
		t.Fatalf("no debería haber seleccionado nada, seleccionó %q", d.SelectedCapability)
	}
	if d.NoExecutionReason == "" {
		t.Error("NO_EXECUTION tiene que venir con su motivo")
	}
}

// Por debajo del umbral mínimo, AGCA se abstiene.
func TestSelector_BelowThresholdDoesNotExecute(t *testing.T) {
	s := NewSelector(NewConfidenceStore())
	s.Minimum = 0.99 // nada va a alcanzarlo

	d := s.Select(contracts(), Intent{Name: "algo_sin_relacion"}, ContextKey{World: "W", Goal: "x"},
		GoalVector("texto sin relación alguna"), Vector{}, nil, allowList{"statistics.search_series": true})

	if d.Executed() {
		t.Fatalf("no debería ejecutar por debajo del umbral, eligió %q con %v", d.SelectedCapability, d.Confidence)
	}
	if d.NoExecutionReason == "" {
		t.Error("falta el motivo del NO_EXECUTION")
	}
}

// La experiencia acumulada tiene que CAMBIAR la selección: dos
// capabilities igual de plausibles, la que tiene mejor historial gana.
func TestSelector_ExperienceChangesFutureSelection(t *testing.T) {
	confidence := NewConfidenceStore()
	s := NewSelector(confidence)

	cs := []tool.Contract{
		{ID: "a.search_series", Tool: "A", Name: "search_series", Category: "statistics"},
		{ID: "b.search_series", Tool: "B", Name: "search_series", Category: "statistics"},
	}
	intent := Intent{Name: "search_series", Goal: "buscar serie"}
	key := ContextKey{World: "W", Goal: intent.Name}
	perms := allowList{"a.search_series": true, "b.search_series": true}

	first := s.Select(cs, intent, key, GoalVector(intent.Goal), Vector{}, nil, perms)
	if !first.Executed() {
		t.Fatalf("debería haber seleccionado alguna: %s", first.NoExecutionReason)
	}

	// La perdedora acumula experiencias excelentes; la ganadora, pésimas.
	loser := "b.search_series"
	if first.SelectedCapability == loser {
		loser = "a.search_series"
	}
	for i := 0; i < 10; i++ {
		confidence.Update(loser, key, 1.0, 0.5)
		confidence.Update(first.SelectedCapability, key, 0.0, 0.5)
	}

	second := s.Select(cs, intent, key, GoalVector(intent.Goal), Vector{}, nil, perms)
	if second.SelectedCapability != loser {
		t.Fatalf("la experiencia no cambió la decisión: sigue eligiendo %q en vez de %q", second.SelectedCapability, loser)
	}
}

func TestContractVector_FromDeclaredContractOnly(t *testing.T) {
	v := ContractVector(tool.Contract{
		ID: "statistics.search_series", Tool: "Statistics", Name: "search_series",
		Category: "statistics", Effects: []string{tool.EffectReadOnly}, Requires: []string{"network.available"},
	})
	for _, want := range []string{"domain.statistics", "operation.search", "operation.series", "read_only", "external_access"} {
		if _, ok := v[want]; !ok {
			t.Errorf("falta la feature %q en %v", want, v.Keys())
		}
	}
	if _, ok := v["mutating"]; ok {
		t.Error("una capability read_only no debería tener la feature mutating")
	}
}
