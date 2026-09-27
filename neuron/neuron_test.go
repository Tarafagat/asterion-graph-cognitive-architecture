package neuron

import (
	"context"
	"testing"
)

func TestDeterministicNeuron_Classifies(t *testing.T) {
	n := NewDeterministicClassifier("Classifier", map[string][]string{
		"read":  {"leer", "consultar"},
		"write": {"crear", "actualizar"},
	})
	out, err := n.Transform(context.Background(), "Necesito consultar el inventario, no crear nada")
	if err != nil {
		t.Fatalf("Transform error: %v", err)
	}
	if out != "read" {
		t.Fatalf("Transform = %q, want \"read\" (1 palabra de write, 1 de read... revisar conteo)", out)
	}
}

func TestDeterministicNeuron_Unknown(t *testing.T) {
	n := NewDeterministicClassifier("Classifier", map[string][]string{
		"read": {"leer"},
	})
	out, err := n.Transform(context.Background(), "esto no tiene ninguna palabra clave")
	if err != nil {
		t.Fatalf("Transform error: %v", err)
	}
	if out != "unknown" {
		t.Fatalf("Transform = %q, want \"unknown\"", out)
	}
}

func TestDeterministicNeuron_Deterministic(t *testing.T) {
	n := NewDeterministicClassifier("Classifier", map[string][]string{
		"read":  {"leer"},
		"write": {"crear"},
	})
	first, _ := n.Transform(context.Background(), "leer y crear crear")
	for i := 0; i < 5; i++ {
		again, _ := n.Transform(context.Background(), "leer y crear crear")
		if again != first {
			t.Fatalf("Transform no es determinista: %q luego %q", first, again)
		}
	}
	if first != "write" {
		t.Fatalf("Transform = %q, want \"write\" (2 ocurrencias contra 1)", first)
	}
}

func TestRegistry_MatchFiltersByCapabilityHealthAndPrefersLocal(t *testing.T) {
	r := NewRegistry()
	local := NewDeterministicClassifier("Local", map[string][]string{"read": {"leer"}})
	r.Register(local)
	r.Register(fakeRemote{name: "Remote", capability: "classification"})
	r.Register(fakeRemote{name: "Unhealthy", capability: "classification", unhealthy: true})
	r.Register(fakeRemote{name: "WrongCap", capability: "reasoning"})

	matches := r.Match("classification")
	if len(matches) != 2 {
		t.Fatalf("Match = %d neuronas, want 2 (Local + Remote, no Unhealthy ni WrongCap)", len(matches))
	}
	if matches[0].Manifest().Name != "Local" {
		t.Fatalf("Match[0] = %q, want \"Local\" (locales antes que remotas)", matches[0].Manifest().Name)
	}
}

type fakeRemote struct {
	name       string
	capability string
	unhealthy  bool
}

func (f fakeRemote) Manifest() Manifest {
	return Manifest{Name: f.name, Provider: "remote-llm", Capabilities: []string{f.capability}, Privacy: "remote", Healthy: !f.unhealthy}
}

func (f fakeRemote) Transform(ctx context.Context, input string) (string, error) {
	return "", ErrNotImplemented
}
