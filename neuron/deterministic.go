package neuron

import (
	"context"
	"sort"
	"strings"
)

// DeterministicNeuron es una neurona real y funcional pero
// deliberadamente simple: clasifica un texto por conteo de palabras clave
// contra un léxico fijo, sin ningún modelo estadístico ni LLM detrás. No
// es un placeholder que "finge" pensar — hace exactamente lo que declara
// (clasificación determinista por keywords) y nada más, cumpliendo el
// contrato Neuron de punta a punta sin ninguna dependencia externa. Sirve
// para MVP 2 del paper ("ejecutar el mismo contrato de tarea" sobre una
// neurona verificable sin credenciales ni modelos pesados) y como
// fallback honesto cuando ninguna neurona GGUF/remota está disponible.
type DeterministicNeuron struct {
	name    string
	lexicon map[string][]string // categoría -> palabras clave (todas en minúscula)
}

// NewDeterministicClassifier crea una DeterministicNeuron con capability
// "classification". lexicon mapea cada categoría a las palabras que la
// activan — ej. {"read": {"leer", "consultar", "ver"}, "write": {"crear",
// "actualizar", "borrar"}}. Un texto que no matchea ninguna categoría se
// clasifica "unknown", nunca se inventa una categoría plausible.
func NewDeterministicClassifier(name string, lexicon map[string][]string) *DeterministicNeuron {
	normalized := make(map[string][]string, len(lexicon))
	for category, words := range lexicon {
		lowered := make([]string, len(words))
		for i, w := range words {
			lowered[i] = strings.ToLower(w)
		}
		normalized[category] = lowered
	}
	return &DeterministicNeuron{name: name, lexicon: normalized}
}

func (n *DeterministicNeuron) Manifest() Manifest {
	return Manifest{
		Name:         n.name,
		Provider:     "deterministic",
		Capabilities: []string{"classification"},
		Privacy:      "local",
		Healthy:      true,
	}
}

// Transform cuenta ocurrencias de palabras clave por categoría y devuelve
// la de mayor conteo — empate se rompe por orden alfabético de la
// categoría, para que el resultado sea 100% reproducible (mismo input,
// mismo output, siempre) — la propiedad que hace que esta neurona sirva
// como ancla de verificación para el resto del runtime.
func (n *DeterministicNeuron) Transform(ctx context.Context, input string) (string, error) {
	lower := strings.ToLower(input)
	counts := map[string]int{}
	for category, words := range n.lexicon {
		for _, w := range words {
			counts[category] += strings.Count(lower, w)
		}
	}

	best, bestCount := "unknown", 0
	categories := make([]string, 0, len(counts))
	for c := range counts {
		categories = append(categories, c)
	}
	sort.Strings(categories)
	for _, c := range categories {
		if counts[c] > bestCount {
			best, bestCount = c, counts[c]
		}
	}
	return best, nil
}
