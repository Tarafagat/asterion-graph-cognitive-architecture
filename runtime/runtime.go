// Package runtime construye, a partir de un *agcaspec.Spec ya compilado,
// UNA Intelligence ejecutable — su Cognitive Graph, su Neuron Registry,
// sus capabilities requeridas — y corre el ciclo cognitivo mínimo del
// Apéndice C del paper ("Camino a la AGI"): perceive -> graph.apply ->
// resolve_goal (acá: fijo, ver doc comment de RunCognitiveCycle) ->
// match neuronas -> ejecutar -> graph.merge -> experience.record.
//
// Simplificaciones deliberadas de este MVP, todas documentadas en vez de
// escondidas: no hay Executive Agent real derivando capabilities desde
// lenguaje natural (Milestone 6 del roadmap) — el caller declara qué
// capability de NEURONA necesita explícitamente. No hay World
// Model/simulación (Milestone 8+ del roadmap) — cada ciclo actúa
// directo, sin evaluar futuros hipotéticos. No hay Policy Engine
// evaluando PolicyDecl en runtime — se compilan y se exponen como datos
// (agcaspec.PolicyDecl), nunca se aplican todavía.
package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/Tarafagat/asterion-language/agcaspec"

	"github.com/Tarafagat/asterion-graph-cognitive-architecture/capability"
	"github.com/Tarafagat/asterion-graph-cognitive-architecture/experience"
	"github.com/Tarafagat/asterion-graph-cognitive-architecture/graph"
	"github.com/Tarafagat/asterion-graph-cognitive-architecture/neuron"
)

// Runtime es una Intelligence AGCA ya materializada — un GraphDecl, un
// Neuron Registry con las neuronas de esa Intelligence (más la
// determinista de referencia del propio runtime, ver Build), y sus
// requerimientos de capability de plugin (todavía no conectados a un
// Capability Registry con proveedores reales — ver capability).
type Runtime struct {
	Intelligence agcaspec.IntelligenceDecl
	GraphSpec    *agcaspec.GraphDecl // nil si el archivo no declaró un AGCA.graph(...) para esta Intelligence
	Graph        *graph.Store
	Neurons      *neuron.Registry
	Capabilities *capability.Registry
	Experience   *experience.Store

	Swarms       []agcaspec.SwarmDecl
	Agents       []agcaspec.AgentDecl
	Memories     []agcaspec.MemoryDecl
	Policies     []agcaspec.PolicyDecl
	Bots         []agcaspec.BotDecl
	RequiredCaps []agcaspec.CapabilityRequirement
}

// referenceLexicon es el léxico por defecto de la DeterministicNeuron de
// referencia que este runtime siempre trae — ver doc comment de Build
// sobre por qué existe incluso si el .asterion no declaró ninguna
// neurona determinista.
var referenceLexicon = map[string][]string{
	"read":    {"leer", "consultar", "ver", "read", "query", "list"},
	"write":   {"crear", "actualizar", "borrar", "escribir", "write", "create", "update", "delete"},
	"analyze": {"analizar", "por qué", "explicar", "analyze", "why", "explain"},
}

// Build construye un Runtime para la Intelligence `varName` de spec. Si
// varName es "" y spec declaró exactamente una Intelligence, usa esa —
// si declaró más de una, varName pasa a ser obligatorio (ambigüedad real,
// no una que este código deba adivinar).
func Build(spec *agcaspec.Spec, varName string) (*Runtime, error) {
	intel, err := resolveIntelligence(spec, varName)
	if err != nil {
		return nil, err
	}

	rt := &Runtime{
		Intelligence: intel,
		Graph:        graph.NewStore(),
		Neurons:      neuron.NewRegistry(),
		Capabilities: capability.NewRegistry(),
		Experience:   experience.NewStore(),
	}

	for _, g := range spec.Graphs {
		if g.Intelligence == intel.VarName {
			gCopy := g
			rt.GraphSpec = &gCopy
			break // AGCA.memory ya exige un único graph= por AGCA.memory(...); tomar el primero alcanza para este MVP.
		}
	}

	for _, n := range spec.Neurons {
		if n.Intelligence == intel.VarName {
			rt.Neurons.Register(placeholderNeuron{decl: n})
		}
	}
	// Neurona de referencia SIEMPRE presente, out-of-band de lo que el
	// .asterion declaró — ver doc comment del paquete: sin esto, un
	// archivo que solo declaró neuronas gguf/remote-llm (sin backend real
	// todavía) no tendría NINGUNA neurona invocable de punta a punta, y
	// el MVP 2 del paper ("ejecutar el mismo contrato de tarea") no
	// tendría nada real que demostrar.
	rt.Neurons.Register(neuron.NewDeterministicClassifier("runtime.DeterministicClassifier", referenceLexicon))

	for _, s := range spec.Swarms {
		if s.Intelligence == intel.VarName {
			rt.Swarms = append(rt.Swarms, s)
		}
	}
	for _, a := range spec.Agents {
		if a.Intelligence == intel.VarName {
			rt.Agents = append(rt.Agents, a)
		}
	}
	for _, m := range spec.Memories {
		if m.Intelligence == intel.VarName {
			rt.Memories = append(rt.Memories, m)
		}
	}
	for _, p := range spec.Policies {
		if p.Intelligence == intel.VarName {
			rt.Policies = append(rt.Policies, p)
		}
	}
	for _, b := range spec.Bots {
		if b.Intelligence == intel.VarName {
			rt.Bots = append(rt.Bots, b)
		}
	}
	for _, c := range spec.Capabilities {
		if c.Intelligence == intel.VarName {
			rt.RequiredCaps = append(rt.RequiredCaps, c)
		}
	}

	return rt, nil
}

func resolveIntelligence(spec *agcaspec.Spec, varName string) (agcaspec.IntelligenceDecl, error) {
	if varName != "" {
		for _, i := range spec.Intelligences {
			if i.VarName == varName {
				return i, nil
			}
		}
		return agcaspec.IntelligenceDecl{}, fmt.Errorf("runtime: no se declaró ninguna AGCA.intelligence(...) de nombre %q", varName)
	}
	switch len(spec.Intelligences) {
	case 0:
		return agcaspec.IntelligenceDecl{}, fmt.Errorf("runtime: el archivo no declaró ninguna AGCA.intelligence(...)")
	case 1:
		return spec.Intelligences[0], nil
	default:
		names := make([]string, len(spec.Intelligences))
		for i, in := range spec.Intelligences {
			names[i] = in.VarName
		}
		return agcaspec.IntelligenceDecl{}, fmt.Errorf(
			"runtime: el archivo declara %d inteligencias (%v) — hace falta indicar cuál con --intelligence", len(spec.Intelligences), names)
	}
}

// CycleResult es lo que devuelve un ciclo cognitivo completo.
type CycleResult struct {
	TraceID         string
	ObservationNode string
	InsightNode     string
	NeuronUsed      string
	Output          string
}

// RunCognitiveCycle implementa el pseudocódigo del Apéndice C, acotado a
// lo que este MVP puede hacer honestamente:
//
//  1. perceive: el goal en sí es la observación — se crea un nodo
//     "observation" en el Cognitive Graph.
//  2. resolve_goal / required_capabilities: en un runtime completo, un
//     Executive Agent derivaría qué CAPACIDAD DE NEURONA hace falta a
//     partir del goal en lenguaje natural (Milestone 6). Ese planner no
//     existe todavía — el caller la indica explícitamente
//     (neuronCapability), en vez de que este código finja inferirla.
//  3. neuron_candidates + selección: Neurons.Match(neuronCapability),
//     probadas en orden (locales antes que remotas) hasta que una
//     responda sin neuron.ErrNotImplemented.
//  4. graph.merge: el resultado se agrega como nodo "insight", con un
//     edge "derived_from" hacia la observación — con procedencia real
//     (qué neurona lo produjo, mismo TraceID que el resto del ciclo).
//  5. experience.record: se registra el ciclo completo, éxito o error.
func (rt *Runtime) RunCognitiveCycle(ctx context.Context, goal, neuronCapability string) (*CycleResult, error) {
	traceID := newTraceID()
	started := time.Now().UTC()

	obs := rt.Graph.AddNode(graph.Node{
		ID:         traceID + ":observation",
		Kind:       "observation",
		Payload:    goal,
		Provenance: graph.Provenance{Source: "perceive", TraceID: traceID},
	})

	candidates := rt.Neurons.Match(neuronCapability)
	if len(candidates) == 0 {
		err := fmt.Errorf("ninguna neurona registrada declara la capacidad %q", neuronCapability)
		rt.Experience.Record(experience.Record{TraceID: traceID, Goal: goal, StartedAt: started, Duration: time.Since(started), Err: err.Error()})
		return nil, err
	}

	var (
		output     string
		usedNeuron string
		lastErr    error
	)
	for _, n := range candidates {
		out, err := n.Transform(ctx, goal)
		if err == nil {
			output, usedNeuron = out, n.Manifest().Name
			break
		}
		lastErr = err
	}
	if usedNeuron == "" {
		err := fmt.Errorf("las %d neurona(s) candidatas para %q fallaron — última: %w", len(candidates), neuronCapability, lastErr)
		rt.Experience.Record(experience.Record{TraceID: traceID, Goal: goal, StartedAt: started, Duration: time.Since(started), Err: err.Error()})
		return nil, err
	}

	insight := rt.Graph.AddNode(graph.Node{
		ID:         traceID + ":insight",
		Kind:       "insight",
		Payload:    output,
		Confidence: 1.0, // sin scoring real de confianza todavía — ver doc comment del paquete
		Provenance: graph.Provenance{Source: "neuron:" + usedNeuron, TraceID: traceID},
	})
	if err := rt.Graph.AddEdge(graph.Edge{From: obs.ID, To: insight.ID, Kind: "derived_from", Provenance: insight.Provenance}); err != nil {
		return nil, fmt.Errorf("runtime: no pude conectar la observación con el insight: %w", err)
	}

	rt.Experience.Record(experience.Record{
		TraceID: traceID, Goal: goal, NeuronUsed: usedNeuron, Result: output,
		StartedAt: started, Duration: time.Since(started),
	})

	return &CycleResult{
		TraceID: traceID, ObservationNode: obs.ID, InsightNode: insight.ID,
		NeuronUsed: usedNeuron, Output: output,
	}, nil
}

// placeholderNeuron respalda una AGCA.neuron(runtime="gguf", ...) o
// AGCA.neuron(adapter="remote-llm", ...) declarada en el .asterion cuyo
// backend real todavía no existe en este repo (ver
// neuron/neuron.go). Su Manifest refleja EXACTAMENTE lo
// declarado, con Healthy=false — "fuera de servicio", un valor real del
// campo Salud del contrato (§ 5.2 del paper), no una mentira: no hay
// ningún proceso GGUF/remoto corriendo detrás. Neurons.Match la excluye
// por eso (ver neuron.Registry.Match), así que nunca es elegida para un
// ciclo cognitivo real — pero SIGUE registrada y visible vía
// Neurons.All(), para que 'agca inspect' pueda listarla como "declarada,
// no disponible" en vez de que desaparezca en silencio. Transform, si
// alguna vez se llama de todos modos, falla con ErrNotImplemented —
// nunca inventa una respuesta en nombre de una neurona que no está
// construida.
type placeholderNeuron struct {
	decl agcaspec.NeuronDecl
}

func (p placeholderNeuron) Manifest() neuron.Manifest {
	provider := p.decl.Runtime
	if provider == "" {
		provider = p.decl.Adapter
	}
	return neuron.Manifest{
		Name:         p.decl.Name,
		Provider:     provider,
		Capabilities: p.decl.Capabilities,
		Privacy:      p.decl.Privacy,
		Healthy:      false, // "no saludable" es honesto: no hay backend real corriendo detrás
	}
}

func (p placeholderNeuron) Transform(ctx context.Context, input string) (string, error) {
	return "", fmt.Errorf("neurona %q (%s): %w", p.decl.Name, p.decl.Runtime+p.decl.Adapter, neuron.ErrNotImplemented)
}

var traceCounter int64

func newTraceID() string {
	traceCounter++
	return fmt.Sprintf("trace-%d-%d", time.Now().UnixNano(), traceCounter)
}
