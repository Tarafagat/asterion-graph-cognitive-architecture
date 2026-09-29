package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/Tarafagat/asterion-language/agcaspec"
	"github.com/Tarafagat/asterion-language/parser"

	"github.com/Tarafagat/asterion-graph-cognitive-architecture/cognition"
	"github.com/Tarafagat/asterion-graph-cognitive-architecture/tool"
)

const actFile = `
language "0.1"

stats = Tool.define(name="Statistics", category="statistics")
search = Tool.capability(tool=stats, name="search_series", effects=["read_only"], guarantees=["returns:series"])

db = Tool.define(name="Database", category="database")
raw = Tool.capability(tool=db, name="raw_sql", effects=["destructive"])

brain = AGCA.intelligence(name="Brain")
world = AGCA.graph(intelligence=brain, name="EconomicResearch")

analyst = AGCA.agent(
    intelligence=brain,
    name="Analyst",
    allow=["statistics.search_series"],
    deny=["database.raw_sql"],
)
`

func buildActRuntime(t *testing.T) *Runtime {
	t.Helper()
	prog, diags := parser.Parse([]byte(actFile), "act.asterion")
	if diags.HasErrors() {
		t.Fatalf("no parsea:\n%s", diags.String())
	}
	spec, diags := agcaspec.Compile(prog, "")
	if diags.HasErrors() {
		t.Fatalf("no compila:\n%s", diags.String())
	}
	rt, err := Build(spec, "brain")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return rt
}

func bindOK(t *testing.T, rt *Runtime, id string) *int {
	t.Helper()
	calls := 0
	err := rt.BindHandler(id, tool.HandlerFunc(func(_ context.Context, _ tool.Input) (tool.ExecutionResult, error) {
		calls++
		return tool.ExecutionResult{Output: tool.Output{"series": []int{1, 2, 3}}}, nil
	}), false)
	if err != nil {
		t.Fatalf("BindHandler(%s): %v", id, err)
	}
	return &calls
}

// Los contratos del .asterion se declaran solos, pero NO son ejecutables
// hasta que alguien ate un handler.
func TestBuild_DeclaresContractsButNotExecutable(t *testing.T) {
	rt := buildActRuntime(t)

	all := rt.Tools.All()
	if len(all) != 2 {
		t.Fatalf("contratos declarados = %d, want 2", len(all))
	}
	if rt.Tools.Implemented("statistics.search_series") {
		t.Error("una capability declarada en el .asterion no debería estar implementada sola")
	}
	if _, _, err := rt.Tools.Resolve("statistics.search_series"); !errors.Is(err, tool.ErrNotImplemented) {
		t.Errorf("err = %v, want ErrNotImplemented", err)
	}
}

// El pipeline completo: decisión, ejecución, experiencia y cambio de
// certeza — todo encadenado y consultable.
func TestAct_FullPipelineProducesDecisionAndExperience(t *testing.T) {
	rt := buildActRuntime(t)
	calls := bindOK(t, rt, "statistics.search_series")

	out, err := rt.Act(context.Background(), ActRequest{Goal: "buscar la serie histórica de precios", Intent: "search_series", Agent: "analyst"})
	if err != nil {
		t.Fatalf("Act: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("el handler se invocó %d veces, want 1", *calls)
	}

	d := out.Decision
	if d.SelectedCapability != "statistics.search_series" {
		t.Errorf("seleccionada = %q", d.SelectedCapability)
	}
	if d.ID == "" || len(d.CandidateCapabilities) == 0 || len(d.ReasoningTrace.Steps) == 0 {
		t.Errorf("la Decision debe traer ID, candidatos y traza: %+v", d)
	}

	// La Experience referencia SU Decision.
	if out.Experience.DecisionID != d.ID {
		t.Errorf("Experience.DecisionID = %q, want %q", out.Experience.DecisionID, d.ID)
	}
	if !out.Experience.Succeeded {
		t.Error("la experiencia debería estar marcada como exitosa")
	}
	if out.Experience.WorldBeforeRef == out.Experience.WorldAfterRef {
		t.Error("el World debería haber cambiado: la acción deja un nodo en el grafo")
	}

	// Una ejecución exitosa modifica la certeza correspondiente.
	if out.Experience.ConfidenceDelta <= 0 {
		t.Errorf("ConfidenceDelta = %v, una ejecución exitosa debería subir la certeza", out.Experience.ConfidenceDelta)
	}
	key := cognition.ContextKey{World: "EconomicResearch", Goal: "search_series"}
	if c, samples := rt.Confidence.Get("statistics.search_series", key); samples != 1 || c <= cognition.PriorConfidence {
		t.Errorf("certeza = (%v, %d samples), debería haber subido con 1 experiencia", c, samples)
	}

	// La Decision quedó consultable.
	stored, err := rt.Decisions.Get(context.Background(), d.ID)
	if err != nil || stored.ID != d.ID {
		t.Errorf("la Decision debería poder recuperarse por ID: %v", err)
	}
}

// Los permisos del agente se verifican ANTES del handler: una capability
// denegada nunca llega a ejecutarse.
func TestAct_DeniedCapabilityNeverReachesHandler(t *testing.T) {
	rt := buildActRuntime(t)
	called := false
	if err := rt.BindHandler("database.raw_sql", tool.HandlerFunc(func(_ context.Context, _ tool.Input) (tool.ExecutionResult, error) {
		called = true
		return tool.ExecutionResult{}, nil
	}), false); err != nil {
		t.Fatalf("BindHandler: %v", err)
	}

	// El goal apunta claramente a la capability denegada.
	_, err := rt.Act(context.Background(), ActRequest{Goal: "ejecutar raw sql sobre la base", Intent: "raw_sql", Agent: "analyst"})
	if err == nil {
		t.Fatal("esperaba que no se ejecutara nada: database.raw_sql está denegada para este agente")
	}
	if called {
		t.Fatal("el handler de una capability denegada NUNCA debe invocarse")
	}
	if !errors.Is(err, ErrNoExecution) {
		t.Errorf("err = %v, want ErrNoExecution", err)
	}
}

// Una capability declarada pero sin handler no se ejecuta — y lo dice.
func TestAct_DeclaredButUnimplementedIsRejected(t *testing.T) {
	rt := buildActRuntime(t)
	_, err := rt.Act(context.Background(), ActRequest{Goal: "buscar la serie histórica", Intent: "search_series", Agent: "analyst"})
	if !errors.Is(err, tool.ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
}

// Aunque no se ejecute nada, la Decision se guarda igual: la
// trazabilidad no depende del resultado.
func TestAct_DecisionPersistedEvenWithoutExecution(t *testing.T) {
	rt := buildActRuntime(t)
	out, err := rt.Act(context.Background(), ActRequest{Goal: "algo totalmente ajeno", Intent: "nada_que_ver", Agent: "analyst"})
	if err == nil {
		t.Fatal("esperaba NO_EXECUTION o similar")
	}
	if out == nil || out.Decision.ID == "" {
		t.Fatal("debería devolver igual la Decision, con su ID")
	}
	list, err := rt.Decisions.List(context.Background(), 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("decisiones guardadas = %d (err=%v), want 1 incluso sin ejecución", len(list), err)
	}
	if list[0].NoExecutionReason == "" {
		t.Error("la Decision guardada debe explicar por qué no se ejecutó")
	}
}

// Un agente inexistente es un error de uso explícito, no un
// deny silencioso.
func TestAct_UnknownAgentIsAnError(t *testing.T) {
	rt := buildActRuntime(t)
	if _, err := rt.Act(context.Background(), ActRequest{Goal: "x", Intent: "y", Agent: "no_existe"}); err == nil {
		t.Fatal("esperaba error por agente inexistente")
	}
}

// Los requirements del contrato se verifican antes del handler.
func TestAct_RequirementsCheckedBeforeHandler(t *testing.T) {
	src := `
language "0.1"
pay = Tool.define(name="Payments", category="payments")
create = Tool.capability(tool=pay, name="create_payment", effects=["external_write"], requires=["terminal.online"])
brain = AGCA.intelligence(name="Brain")
a = AGCA.agent(intelligence=brain, name="A", allow=["payments.create_payment"])
`
	prog, _ := parser.Parse([]byte(src), "pay.asterion")
	spec, diags := agcaspec.Compile(prog, "")
	if diags.HasErrors() {
		t.Fatalf("no compila: %s", diags.String())
	}
	rt, err := Build(spec, "brain")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	called := false
	_ = rt.BindHandler("payments.create_payment", tool.HandlerFunc(func(_ context.Context, _ tool.Input) (tool.ExecutionResult, error) {
		called = true
		return tool.ExecutionResult{Output: tool.Output{"payment_id": "x"}}, nil
	}), false)

	// Sin verificador de requirements, no se ejecuta.
	if _, err := rt.Act(context.Background(), ActRequest{Goal: "crear un pago", Intent: "create_payment", Agent: "a"}); !errors.Is(err, tool.ErrRequirementUnsatisfied) {
		t.Fatalf("err = %v, want ErrRequirementUnsatisfied", err)
	}
	if called {
		t.Fatal("el handler no debe invocarse con requirements sin verificar")
	}

	// Con el requirement satisfecho, sí.
	rt.Requirements = tool.RequirementFunc(func(_ context.Context, req string) bool { return req == "terminal.online" })
	if _, err := rt.Act(context.Background(), ActRequest{Goal: "crear un pago", Intent: "create_payment", Agent: "a"}); err != nil {
		t.Fatalf("con el requirement satisfecho debería ejecutar: %v", err)
	}
	if !called {
		t.Fatal("el handler debería haberse invocado")
	}
}

// La persistencia hace que la certeza sobreviva entre runtimes — el
// Segundo Principio no serviría de nada si cada corrida empezara de cero.
func TestAct_ConfidenceSurvivesAcrossRuntimes(t *testing.T) {
	dir := t.TempDir()
	key := cognition.ContextKey{World: "EconomicResearch", Goal: "search_series"}

	first := buildActRuntime(t)
	if err := first.WithPersistence(dir); err != nil {
		t.Fatalf("WithPersistence: %v", err)
	}
	bindOK(t, first, "statistics.search_series")
	if _, err := first.Act(context.Background(), ActRequest{Goal: "buscar la serie histórica de precios", Intent: "search_series", Agent: "analyst"}); err != nil {
		t.Fatalf("Act: %v", err)
	}
	learned, _ := first.Confidence.Get("statistics.search_series", key)

	// Un runtime nuevo, mismo directorio: recuerda.
	second := buildActRuntime(t)
	if err := second.WithPersistence(dir); err != nil {
		t.Fatalf("WithPersistence: %v", err)
	}
	restored, samples := second.Confidence.Get("statistics.search_series", key)
	if restored != learned {
		t.Errorf("certeza restaurada = %v, want %v (lo aprendido debe sobrevivir a la corrida)", restored, learned)
	}
	if samples != 0 {
		// El conteo de muestras es de esta corrida; la certeza es lo que persiste.
		t.Logf("samples en el runtime nuevo = %d (esperado: el valor persiste, el conteo es por corrida)", samples)
	}

	decisions, err := second.Decisions.List(context.Background(), 10)
	if err != nil || len(decisions) != 1 {
		t.Errorf("decisiones persistidas = %d (err=%v), want 1", len(decisions), err)
	}
}
