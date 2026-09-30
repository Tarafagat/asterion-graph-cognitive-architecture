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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tarafagat/asterion-language/agcaspec"
	"github.com/Tarafagat/asterion-plugin-contract/apc"

	"github.com/Tarafagat/asterion-graph-cognitive-architecture/capability"
	"github.com/Tarafagat/asterion-graph-cognitive-architecture/cognition"
	"github.com/Tarafagat/asterion-graph-cognitive-architecture/experience"
	"github.com/Tarafagat/asterion-graph-cognitive-architecture/graph"
	"github.com/Tarafagat/asterion-graph-cognitive-architecture/neuron"
	"github.com/Tarafagat/asterion-graph-cognitive-architecture/tool"
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
	RoleDecls    []agcaspec.RoleDecl
	RequiredCaps []agcaspec.CapabilityRequirement

	// Secrets/Imports son globales al ARCHIVO, no por Intelligence (mismo
	// criterio que el paper: `secret ...{}` vive afuera de cualquier
	// bloque `intelligence{}` — ver Apéndice B) — Build copia el spec
	// entero acá, sin filtrar por esta Intelligence.
	Secrets []agcaspec.SecretDecl
	Imports []agcaspec.ImportDecl

	// DiscoveredCapabilities/DiscoveredSecrets son el resultado de leer
	// de verdad el plugin.yaml de cada plugin que Imports trajo (con
	// route local) — ver discoverImportedPlugins. Las capabilities con
	// Resolved==true YA quedaron registradas en Capabilities; ninguna de
	// las dos listas hace falta consultarla para que
	// Capabilities.Match/RunCognitiveCycle funcionen — son para poder
	// INSPECCIONAR qué se descubrió (y qué no, con el motivo).
	DiscoveredCapabilities []DiscoveredCapability
	DiscoveredSecrets      []DiscoveredSecret

	// --- Capa de Experience (Segundo Principio de AGI) -------------------
	//
	// Tools es el ÚNICO registro de capabilities ejecutables: lo que no
	// está acá no se puede ejecutar, punto (ver package tool). Selector
	// arma la Decision, Evaluator la juzga después de ejecutar, Learner
	// convierte esa evaluación en un cambio de certeza, y Confidence
	// guarda esa certeza POR CONTEXTO (nunca un número global por
	// capability).
	Tools       *tool.Registry
	Selector    *cognition.Selector
	Evaluator   cognition.Evaluator
	Learner     cognition.Learner
	Confidence  *cognition.ConfidenceStore
	Decisions   cognition.DecisionStore
	Experiences ExperienceSink

	// Requirements verifica las precondiciones declaradas en un contrato
	// (requires). nil significa que ningún requirement puede verificarse
	// — y entonces un contrato que declare alguno NO se ejecuta.
	Requirements tool.RequirementChecker

	// isolatedHandlers marca qué capabilities de Tools con isolation
	// fueron registradas confirmando que corren aisladas.
	isolatedHandlers map[string]bool

	// persist, cuando no es nil, es el FileStore que hace que decisiones,
	// experiencias y certeza SOBREVIVAN entre corridas del CLI.
	persist *cognition.FileStore
}

// ExperienceSink es lo que el runtime necesita de un store de
// experiencias — separado de cognition.ExperienceStore para que el
// runtime no dependa de los métodos de búsqueda, solo de poder guardar.
type ExperienceSink interface {
	SaveExperience(ctx context.Context, e cognition.Experience) error
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

	confidence := cognition.NewConfidenceStore()
	memory := cognition.NewMemoryStore()
	rt := &Runtime{
		Intelligence:     intel,
		Graph:            graph.NewStore(),
		Neurons:          neuron.NewRegistry(),
		Capabilities:     capability.NewRegistry(),
		Experience:       experience.NewStore(),
		Tools:            tool.NewRegistry(),
		Confidence:       confidence,
		Selector:         cognition.NewSelector(confidence),
		Evaluator:        cognition.NewStandardEvaluator(),
		Learner:          cognition.NewEMALearner(),
		Decisions:        memory,
		Experiences:      memory,
		isolatedHandlers: map[string]bool{},
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
	for _, r := range spec.Roles {
		if r.Intelligence == intel.VarName {
			rt.RoleDecls = append(rt.RoleDecls, r)
		}
	}
	for _, c := range spec.Capabilities {
		if c.Intelligence == intel.VarName {
			rt.RequiredCaps = append(rt.RequiredCaps, c)
		}
	}
	// Contratos de Tool declarados en el .asterion — se DECLARAN acá
	// (visibles, puntuables) pero quedan sin handler hasta que alguien
	// los ate con BindHandler: una capability declarada y no implementada
	// nunca es ejecutable, y el runtime lo dice en vez de improvisar.
	toolByVar := map[string]agcaspec.ToolDecl{}
	for _, t := range spec.Tools {
		toolByVar[t.VarName] = t
	}
	for _, c := range spec.ToolCaps {
		owner := toolByVar[c.Tool]
		if err := rt.Tools.Declare(tool.Contract{
			ID: c.ID, Tool: owner.Name, Name: c.Name, Description: c.Description,
			Category: owner.Category, Input: c.Input, Output: c.Output,
			Effects: c.Effects, Requires: c.Requires, Guarantees: c.Guarantees,
			Isolation: owner.Isolation,
		}); err != nil {
			return nil, fmt.Errorf("runtime: no pude declarar la capability %q: %w", c.ID, err)
		}
	}

	rt.Secrets = spec.Secrets
	rt.Imports = spec.Imports
	rt.DiscoveredCapabilities, rt.DiscoveredSecrets = discoverImportedPlugins(spec.Imports, rt.Capabilities)

	return rt, nil
}

// DiscoveredCapability es un plugin traído por Import(...) cuyas
// capabilities se intentó derivar de verdad — Resolved dice si se pudo
// (y, si se pudo, quedó YA registrado en rt.Capabilities) o no (con el
// motivo en Reason) — 'graph inspect' lo muestra tal cual, nunca
// silenciado: un plugin de route remota sin clonar, o con un plugin.yaml
// inválido, aparece igual, marcado como no resuelto.
type DiscoveredCapability struct {
	ImportVar    string
	Plugin       string
	Route        string
	Resolved     bool
	Reason       string // motivo si !Resolved
	Capabilities []string
}

// DiscoveredSecret es un campo secreto que un plugin YA declaró en su
// propio config_schema (secret: true) — descubierto automáticamente vía
// Import(...), SIN que el archivo AGCA tenga que repetir esa
// información con un AGCA.secret(from=, field=) manual. Ver el doc
// comment de discoverImportedPlugins sobre cuándo SÍ conviene declarar
// un AGCA.secret(...) explícito de todos modos (para nombrarlo/exponerlo
// a un bot puntual) en vez de conformarse con este descubrimiento.
type DiscoveredSecret struct {
	ImportVar string
	Plugin    string
	Key       string
	Label     string
}

// discoverImportedPlugins recorre cada Import(...) del archivo y, para
// cada plugin cuya route sea una carpeta LOCAL (existe en disco — misma
// heurística que resolveOrInstall en asterion-core: nunca asume que una
// route con forma de URL de git ya está clonada), lee su plugin.yaml de
// verdad UNA sola vez y deriva de ahí dos cosas distintas:
//
//   - sus capabilities (resources[].crud + actions[], ver
//     capability.DeriveFromManifest) — se registran YA en caps, listas
//     para que Neurons/Capabilities.Match las use en el próximo ciclo
//     cognitivo, sin que el archivo AGCA tenga que declarar nada más;
//   - sus campos de config marcados secret:true (config_schema) — se
//     reportan como DiscoveredSecret, informativos: el plugin YA sabe
//     que ese campo es secreto, no hace falta un AGCA.secret(from=,
//     field=) manual solo para enterarse de que existe.
//
// Un AGCA.secret(from=, field=) explícito SIGUE teniendo sentido cuando
// hace falta un NOMBRE propio de la Intelligence para ese secreto —
// típicamente para listarlo en los permissions de un AGCA.bot(...) (ver
// examples/agca-company.asterion) — nunca solo para repetir información
// que este descubrimiento ya deja disponible.
//
// Una route de git (sin clonar) o un plugin.yaml inválido no son un
// error fatal de Build: el plugin queda con Resolved=false (y sin
// ningún DiscoveredSecret), con el motivo en Reason, para que 'graph
// inspect' lo muestre honestamente en vez de que el runtime entero
// falle por un plugin que este MVP todavía no sabe resolver.
func discoverImportedPlugins(imports []agcaspec.ImportDecl, caps *capability.Registry) ([]DiscoveredCapability, []DiscoveredSecret) {
	var discCaps []DiscoveredCapability
	var discSecrets []DiscoveredSecret
	for _, imp := range imports {
		for _, pluginName := range imp.PluginNames {
			route := imp.PluginRoutes[pluginName]
			dc := DiscoveredCapability{ImportVar: imp.VarName, Plugin: pluginName, Route: route}

			routeAbs := route
			if !filepath.IsAbs(route) {
				routeAbs = filepath.Join(imp.ResolvedDir, route)
			}
			info, statErr := os.Stat(routeAbs)
			if statErr != nil || !info.IsDir() {
				dc.Reason = fmt.Sprintf("route %q no es una carpeta local (¿todavía no se clonó? Import no clona git, solo lee plugin.yaml ya presente en disco)", route)
				discCaps = append(discCaps, dc)
				continue
			}

			manifest, err := apc.LoadManifest(routeAbs)
			if err != nil {
				dc.Reason = err.Error()
				discCaps = append(discCaps, dc)
				continue
			}

			provider := capability.DeriveFromManifest(pluginName, manifest)
			caps.Register(provider)
			dc.Resolved = true
			dc.Capabilities = provider.Capabilities
			discCaps = append(discCaps, dc)

			for _, field := range manifest.ConfigSchema {
				if field.IsSecret() {
					discSecrets = append(discSecrets, DiscoveredSecret{
						ImportVar: imp.VarName, Plugin: pluginName, Key: field.Key, Label: field.Label,
					})
				}
			}
		}
	}
	return discCaps, discSecrets
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

// BindHandler ata la implementación real de una capability YA declarada
// en el .asterion. Es el único camino para que algo sea ejecutable: el
// archivo declara el contrato, el código Go ata el handler, y AGCA solo
// puede elegir entre los IDs que existan en ese cruce.
//
// isolated=true es la confirmación explícita de que ESTE handler corre
// aislado — obligatorio para una capability de una Tool con
// isolation declarada (ver tool.RequireIsolation): sin esa confirmación,
// una capability de ejecución de código no se ejecuta, nunca hereda los
// permisos del proceso host por omisión.
func (rt *Runtime) BindHandler(capabilityID string, handler tool.Handler, isolated bool) error {
	if err := rt.Tools.Bind(capabilityID, handler); err != nil {
		return err
	}
	if isolated {
		rt.isolatedHandlers[capabilityID] = true
	}
	return nil
}

// WithPersistence hace que decisiones, experiencias y certeza aprendida
// SOBREVIVAN entre corridas — sin esto, cada invocación del CLI sería
// amnésica y el Segundo Principio (la experiencia cambia decisiones
// futuras) no podría cumplirse de una corrida a la otra. Carga lo que ya
// hubiera guardado de antes.
func (rt *Runtime) WithPersistence(dir string) error {
	store, err := cognition.NewFileStore(dir)
	if err != nil {
		return err
	}
	if err := store.LoadConfidence(rt.Confidence); err != nil {
		return err
	}
	rt.persist = store
	rt.Decisions = store
	rt.Experiences = store
	return nil
}

// PersistenceDir devuelve dónde persiste, o "" si esta Intelligence corre
// sin memoria entre corridas.
func (rt *Runtime) PersistenceDir() string {
	if rt.persist == nil {
		return ""
	}
	return rt.persist.Dir()
}

// DefaultPersistenceDir es dónde vive la memoria de una Intelligence:
// ~/.config/asterion/agca/<intelligence>/ — mismo criterio de ubicación
// que el resto del estado local de Asterion.
func DefaultPersistenceDir(intelligenceName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, intelligenceName)
	return filepath.Join(home, ".config", "asterion", "agca", safe), nil
}
