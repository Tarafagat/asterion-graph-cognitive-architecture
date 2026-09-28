package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tarafagat/asterion-language/agcaspec"
	"github.com/Tarafagat/asterion-language/parser"
)

const testFile = `
language "0.1"

brain = AGCA.intelligence(name="TestBrain")
world = AGCA.graph(intelligence=brain, name="TestWorld", hierarchical=true)
gguf_neuron = AGCA.neuron(intelligence=brain, name="GGUFStub", runtime="gguf", capabilities=["reasoning"], privacy="local")
memory = AGCA.memory(intelligence=brain, name="Mem", graph=world)
admin_bot = AGCA.bot(intelligence=brain, name="AdminBot", interface="terminal", permissions=["inventory.read"])
AGCA.requires_capability(intelligence=brain, capability="database.query")
`

func buildTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	prog, diags := parser.Parse([]byte(testFile), "test.asterion")
	if diags.HasErrors() {
		t.Fatalf("no parsea:\n%s", diags.String())
	}
	spec, diags := agcaspec.Compile(prog, "")
	if diags.HasErrors() {
		t.Fatalf("no compila:\n%s", diags.String())
	}
	rt, err := Build(spec, "")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	return rt
}

func TestBuild_WiresEverythingForItsIntelligence(t *testing.T) {
	rt := buildTestRuntime(t)
	if rt.Intelligence.Name != "TestBrain" {
		t.Errorf("Intelligence.Name = %q", rt.Intelligence.Name)
	}
	if rt.GraphSpec == nil || rt.GraphSpec.Name != "TestWorld" {
		t.Fatalf("GraphSpec = %+v", rt.GraphSpec)
	}
	if len(rt.Memories) != 1 || len(rt.Bots) != 1 || len(rt.RequiredCaps) != 1 {
		t.Errorf("Memories=%d Bots=%d RequiredCaps=%d, want 1/1/1", len(rt.Memories), len(rt.Bots), len(rt.RequiredCaps))
	}

	// La neurona gguf declarada está registrada pero no saludable (sin
	// backend real) — no debe aparecer en Match, sí en All.
	all := rt.Neurons.All()
	if len(all) != 2 { // GGUFStub (placeholder) + runtime.DeterministicClassifier
		t.Fatalf("Neurons.All() = %d, want 2", len(all))
	}
	matches := rt.Neurons.Match("reasoning")
	if len(matches) != 0 {
		t.Fatalf("Match(reasoning) = %d, want 0 (GGUFStub no está saludable, no hay backend real)", len(matches))
	}
}

func TestBuild_AmbiguousIntelligenceRequiresName(t *testing.T) {
	prog, diags := parser.Parse([]byte(`
a = AGCA.intelligence(name="A")
b = AGCA.intelligence(name="B")
`), "test.asterion")
	if diags.HasErrors() {
		t.Fatalf("no parsea: %s", diags.String())
	}
	spec, diags := agcaspec.Compile(prog, "")
	if diags.HasErrors() {
		t.Fatalf("no compila: %s", diags.String())
	}
	if _, err := Build(spec, ""); err == nil {
		t.Fatal("esperaba un error por ambigüedad (2 inteligencias, sin especificar cuál)")
	}
	rt, err := Build(spec, "b")
	if err != nil {
		t.Fatalf("Build(\"b\") error: %v", err)
	}
	if rt.Intelligence.Name != "B" {
		t.Errorf("Intelligence.Name = %q, want \"B\"", rt.Intelligence.Name)
	}
}

func TestRunCognitiveCycle_UsesReferenceNeuronAndUpdatesGraph(t *testing.T) {
	rt := buildTestRuntime(t)
	result, err := rt.RunCognitiveCycle(context.Background(), "necesito consultar el inventario", "classification")
	if err != nil {
		t.Fatalf("RunCognitiveCycle error: %v", err)
	}
	if result.NeuronUsed != "runtime.DeterministicClassifier" {
		t.Errorf("NeuronUsed = %q, want \"runtime.DeterministicClassifier\" (única neurona saludable con esa capacidad)", result.NeuronUsed)
	}
	if result.Output != "read" {
		t.Errorf("Output = %q, want \"read\"", result.Output)
	}

	obsNode, ok := rt.Graph.Node(result.ObservationNode)
	if !ok || obsNode.Kind != "observation" {
		t.Fatalf("nodo de observación no se guardó bien: %+v ok=%v", obsNode, ok)
	}
	insightNode, ok := rt.Graph.Node(result.InsightNode)
	if !ok || insightNode.Kind != "insight" || insightNode.Payload != "read" {
		t.Fatalf("nodo de insight no se guardó bien: %+v ok=%v", insightNode, ok)
	}

	edges := rt.Graph.EdgesFrom(result.ObservationNode)
	if len(edges) != 1 || edges[0].To != result.InsightNode || edges[0].Kind != "derived_from" {
		t.Fatalf("edges = %+v", edges)
	}

	if rt.Experience.Len() != 1 {
		t.Fatalf("Experience.Len() = %d, want 1", rt.Experience.Len())
	}
	rec := rt.Experience.All()[0]
	if rec.NeuronUsed != "runtime.DeterministicClassifier" || rec.Result != "read" || rec.Err != "" {
		t.Errorf("Experience record = %+v", rec)
	}
}

func TestRunCognitiveCycle_NoNeuronForCapability(t *testing.T) {
	rt := buildTestRuntime(t)
	_, err := rt.RunCognitiveCycle(context.Background(), "algo", "capacidad-inexistente")
	if err == nil {
		t.Fatal("esperaba un error: ninguna neurona declara esa capacidad")
	}
	if rt.Experience.Len() != 1 {
		t.Fatalf("Experience.Len() = %d, want 1 (el fallo también se registra)", rt.Experience.Len())
	}
}

const systemWithLocalPlugin = `
language "0.1"

db = System.plugin(route="./tutorial-db-plugin", principal=true)
api = System.plugin(route="git://example.com/never-cloned.git")
`

func writeSystemFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("no pude escribir %s: %v", path, err)
	}
	return path
}

func writeMinimalPluginYAML(t *testing.T, dir string) {
	t.Helper()
	content := `name: tutorial-db-plugin
version: "1.0.0"
start:
  command: ./tutorial-db-plugin
port: 0
config_schema:
  - key: database_name
    label: "Nombre de la base de datos"
    type: string
  - key: database_password
    label: "Contraseña"
    type: string
    secret: true
resources:
  - name: invoices
    endpoint: /invoices
    crud: [create, read]
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("no pude escribir plugin.yaml: %v", err)
	}
}

func TestBuild_DerivesCapabilitiesFromLocalImportedPlugin(t *testing.T) {
	dir := t.TempDir()
	writeSystemFile(t, dir, "system.asterion", systemWithLocalPlugin)
	if err := os.Mkdir(filepath.Join(dir, "tutorial-db-plugin"), 0o755); err != nil {
		t.Fatalf("no pude crear tutorial-db-plugin/: %v", err)
	}
	writeMinimalPluginYAML(t, filepath.Join(dir, "tutorial-db-plugin"))

	prog, diags := parser.Parse([]byte(`
sys = Import(path="./system.asterion")
brain = AGCA.intelligence(name="Brain")
`), "test.asterion")
	if diags.HasErrors() {
		t.Fatalf("no parsea: %s", diags.String())
	}
	spec, diags := agcaspec.Compile(prog, dir)
	if diags.HasErrors() {
		t.Fatalf("no compila: %s", diags.String())
	}
	rt, err := Build(spec, "brain")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	if len(rt.DiscoveredCapabilities) != 2 {
		t.Fatalf("DiscoveredCapabilities = %d, want 2 (db resuelto, api no)", len(rt.DiscoveredCapabilities))
	}
	var dbDisc, apiDisc DiscoveredCapability
	for _, dc := range rt.DiscoveredCapabilities {
		switch dc.Plugin {
		case "db":
			dbDisc = dc
		case "api":
			apiDisc = dc
		}
	}
	if !dbDisc.Resolved || len(dbDisc.Capabilities) != 2 {
		t.Fatalf("db discovery = %+v, want Resolved=true con 2 capabilities", dbDisc)
	}
	if apiDisc.Resolved {
		t.Fatalf("api discovery = %+v, want Resolved=false (route de git, nunca clonada)", apiDisc)
	}
	if apiDisc.Reason == "" {
		t.Error("api discovery no dio ningún motivo de por qué no se resolvió")
	}

	// Confirmar que quedó REGISTRADO de verdad en el Capability Registry
	// (no solo reportado) — Match tiene que encontrarlo.
	matches := rt.Capabilities.Match("invoices.create")
	if len(matches) != 1 || matches[0].Name != "db" {
		t.Fatalf("Capabilities.Match(invoices.create) = %+v, want [{db ...}]", matches)
	}

	// El campo secret:true del propio config_schema de "db" se descubre
	// SOLO — sin que este .asterion haya declarado ningún AGCA.secret(...).
	if len(rt.DiscoveredSecrets) != 1 {
		t.Fatalf("DiscoveredSecrets = %+v, want 1 (database_password, marcado secret:true)", rt.DiscoveredSecrets)
	}
	ds := rt.DiscoveredSecrets[0]
	if ds.Plugin != "db" || ds.Key != "database_password" {
		t.Errorf("DiscoveredSecrets[0] = %+v", ds)
	}
}
