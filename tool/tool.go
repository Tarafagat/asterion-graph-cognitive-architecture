// Package tool implementa la frontera de autoridad de AGCA: una Tool es
// un conjunto CERRADO de capabilities declaradas, cada una con su
// contrato (effects, requires, guarantees) y su handler registrado de
// antemano. AGCA selecciona un CapabilityID; el runtime lo resuelve
// contra ese registro. Nunca hay un camino desde una decisión cognitiva
// hacia código arbitrario:
//
//	AGCA -> CapabilityID -> Tool Contract -> Handler predefinido -> Ejecución
//
// y NUNCA:
//
//	AGCA -> string arbitrario -> eval/exec/shell/SQL crudo
//
// Esto es estructural, no una convención: la única forma de ejecutar algo
// es Registry.Resolve(ref), que falla con ErrUnknownCapability si ese ID
// no fue registrado con un handler real. No existe ninguna función en
// este paquete que acepte código, un comando, una query o una ruta
// dinámica y la ejecute — una Tool de estadística puede usar SQL por
// dentro, pero "statistics.raw_sql" simplemente no existe como capability
// salvo que alguien la declare y registre explícitamente.
//
// Distinto de internal/capability (paquete hermano): ese descubre qué
// capabilities declara un PLUGIN ya instalado (leyendo su plugin.yaml,
// para el Capability Router del paper § 7.1). Este describe capabilities
// EJECUTABLES con handler propio dentro del runtime cognitivo. Comparten
// la idea de "capacidad declarada", no la implementación.
package tool

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Errores que el runtime distingue para poder explicarlos en una
// Decision — nunca se colapsan en un error genérico.
var (
	// ErrUnknownCapability es lo que hace que la frontera sea real: un ID
	// que nadie registró no se ejecuta, no se aproxima al más parecido, y
	// no se interpreta como algo más. Simplemente no existe.
	ErrUnknownCapability = errors.New("tool: capability no registrada — AGCA solo puede invocar capabilities declaradas explícitamente")

	// ErrRequirementUnsatisfied es una precondición declarada en el
	// contrato (requires) que no se cumple al momento de ejecutar.
	ErrRequirementUnsatisfied = errors.New("tool: requirement del contrato no satisfecho")

	// ErrNotImplemented distingue "esta capability no existe" de "existe
	// como contrato declarado en el .asterion, pero nadie registró un
	// handler que la implemente". Las dos impiden ejecutar; solo la
	// segunda significa que el contrato es real y falta el código.
	ErrNotImplemented = errors.New("tool: capability declarada pero sin handler registrado — no es ejecutable")
)

// Effects conocidos — mismo vocabulario cerrado que valida el compilador
// (agcaspec.checkEffectConflict). Cualquier otro effect es un tag libre
// que el contrato puede declarar y una política puede mirar, pero que
// este paquete no interpreta.
const (
	EffectReadOnly      = "read_only"
	EffectPure          = "pure"
	EffectMutating      = "mutating"
	EffectExternalWrite = "external_write"
	EffectDestructive   = "destructive"
)

// Contract es el contrato declarado de UNA capability — la traducción en
// runtime de un Tool.capability(...) de Asterion Language.
type Contract struct {
	ID          string // "<tool>.<name>", estable — lo que selecciona una Decision
	Tool        string
	Name        string
	Description string
	Category    string
	Input       []string
	Output      []string
	Effects     []string
	Requires    []string
	Guarantees  []string
	// Isolation viene de la Tool ("sandbox" para una Tool de ejecución de
	// código) — el registro NO lo fuerza por sí solo: quien registre un
	// handler de una Tool con Isolation != "" es responsable de que ese
	// handler de verdad corra aislado. Ver RequireIsolation.
	Isolation string
}

// IsReadOnly responde si el contrato promete no modificar el World —
// consultado ANTES de ejecutar (por políticas y por el pipeline de
// permisos), nunca después.
func (c Contract) IsReadOnly() bool {
	for _, e := range c.Effects {
		if e == EffectReadOnly || e == EffectPure {
			return true
		}
	}
	return false
}

// IsMutating responde si el contrato declara algún efecto que cambia el
// World o algo externo.
func (c Contract) IsMutating() bool {
	for _, e := range c.Effects {
		switch e {
		case EffectMutating, EffectExternalWrite, EffectDestructive:
			return true
		}
	}
	return false
}

// HasEffect responde si el contrato declara un effect puntual — sirve
// tanto para los conocidos como para un tag libre ("financial_operation",
// "creates:Payment").
func (c Contract) HasEffect(effect string) bool {
	for _, e := range c.Effects {
		if e == effect {
			return true
		}
	}
	return false
}

// Input es lo que recibe un handler: datos tipados por clave, nunca
// código ni una expresión a evaluar. Es deliberadamente un mapa de
// valores y no un string libre — no hay forma de "pasarle un comando" a
// un handler a través de esta API.
type Input map[string]any

// Output es lo que devuelve un handler.
type Output map[string]any

// ExecutionResult es el resultado de invocar un handler, con lo que la
// evaluación posterior necesita para juzgarlo (ver cognition.Evaluator):
// si falló, qué devolvió, y cuánto tardó lo mide el caller.
type ExecutionResult struct {
	CapabilityID string
	Output       Output
	Err          error
	// SelfEvaluation es la autoevaluación OPCIONAL de la propia Tool
	// (0..1). Nunca es la verdad final: el evaluador la combina con
	// evaluaciones independientes (contrato/goal/world) — ver
	// cognition.Evaluator y el § 13 del pedido de Experience.
	SelfEvaluation *float64
	// Metrics son métricas libres que la Tool quiera reportar.
	Metrics map[string]float64
}

// Succeeded responde si la ejecución terminó sin error.
func (r ExecutionResult) Succeeded() bool { return r.Err == nil }

// Handler ejecuta UNA capability. Recibe datos, devuelve datos — nunca
// recibe código a ejecutar. Un handler concreto puede usar SQL, la red o
// un subproceso por dentro; lo que no puede es dejar que el llamador
// cognitivo elija ESO: solo puede elegir el CapabilityID que llega hasta
// acá.
type Handler interface {
	Execute(ctx context.Context, in Input) (ExecutionResult, error)
}

// HandlerFunc adapta una función al contrato Handler.
type HandlerFunc func(ctx context.Context, in Input) (ExecutionResult, error)

func (f HandlerFunc) Execute(ctx context.Context, in Input) (ExecutionResult, error) {
	return f(ctx, in)
}

// RequirementChecker responde si una precondición declarada en el
// contrato (ej. "network.available", "terminal.online") se cumple ahora.
// Un requirement sin checker registrado se considera NO satisfecho — el
// default seguro: nunca se asume que algo está disponible porque nadie
// supo verificarlo.
type RequirementChecker interface {
	Satisfied(ctx context.Context, requirement string) bool
}

// RequirementFunc adapta una función a RequirementChecker.
type RequirementFunc func(ctx context.Context, requirement string) bool

func (f RequirementFunc) Satisfied(ctx context.Context, requirement string) bool {
	return f(ctx, requirement)
}

// Registry es el registro explícito de capabilities ejecutables: un
// contrato + un handler por ID. Es la única fuente de ejecución posible
// del runtime cognitivo.
type Registry struct {
	mu        sync.RWMutex
	contracts map[string]Contract
	handlers  map[string]Handler
	byTool    map[string][]string
}

// NewRegistry crea un Capability Registry vacío.
func NewRegistry() *Registry {
	return &Registry{
		contracts: map[string]Contract{},
		handlers:  map[string]Handler{},
		byTool:    map[string][]string{},
	}
}

// Declare registra SOLO el contrato de una capability, sin handler —
// es lo que produce un Tool.capability(...) de un .asterion: el archivo
// declara qué sabe hacer una Tool, pero el código que lo hace se ata
// aparte con Bind. Una capability declarada y no atada es visible
// (inspeccionable, puntuable) pero NO ejecutable: Resolve devuelve
// ErrNotImplemented, nunca un handler improvisado.
func (r *Registry) Declare(contract Contract) error {
	if contract.ID == "" {
		return fmt.Errorf("tool: contrato sin ID — toda capability necesita un identificador estable (\"<tool>.<name>\")")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.contracts[contract.ID]; exists {
		return fmt.Errorf("tool: capability %q ya declarada", contract.ID)
	}
	r.contracts[contract.ID] = contract
	r.byTool[contract.Tool] = append(r.byTool[contract.Tool], contract.ID)
	return nil
}

// Bind ata un handler a un contrato YA declarado. Falla si ese ID no fue
// declarado antes (no se puede implementar algo que ningún contrato
// describe) o si ya tenía handler (nunca se reemplaza silenciosamente).
func (r *Registry) Bind(id string, handler Handler) error {
	if handler == nil {
		return fmt.Errorf("tool: handler nil para %q", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, declared := r.contracts[id]; !declared {
		return fmt.Errorf("%w: %q — no se puede atar un handler a una capability que ningún contrato declaró", ErrUnknownCapability, id)
	}
	if _, bound := r.handlers[id]; bound {
		return fmt.Errorf("tool: capability %q ya tiene handler — no se puede reemplazar", id)
	}
	r.handlers[id] = handler
	return nil
}

// Implemented responde si una capability declarada tiene handler.
func (r *Registry) Implemented(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.handlers[id]
	return ok
}

// Register asocia un contrato con su handler. Falla si el contrato no
// tiene ID, si el handler es nil, o si ese ID ya estaba registrado —
// nunca se pisa silenciosamente un handler por otro, porque eso sería
// exactamente la forma de colar una implementación distinta detrás de un
// ID en el que una Decision ya confía.
func (r *Registry) Register(contract Contract, handler Handler) error {
	if contract.ID == "" {
		return fmt.Errorf("tool: contrato sin ID — toda capability necesita un identificador estable (\"<tool>.<name>\")")
	}
	if handler == nil {
		return fmt.Errorf("tool: capability %q sin handler — una capability declarada pero no implementada nunca se registra (sería seleccionable y no ejecutable)", contract.ID)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.contracts[contract.ID]; exists {
		return fmt.Errorf("tool: capability %q ya registrada — no se puede reemplazar su handler", contract.ID)
	}
	r.contracts[contract.ID] = contract
	r.handlers[contract.ID] = handler
	r.byTool[contract.Tool] = append(r.byTool[contract.Tool], contract.ID)
	return nil
}

// Resolve devuelve el contrato y el handler de un ID. Un ID no registrado
// da ErrUnknownCapability — sin fuzzy matching, sin fallback, sin
// interpretación: esa es la frontera.
func (r *Registry) Resolve(id string) (Contract, Handler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	contract, ok := r.contracts[id]
	if !ok {
		return Contract{}, nil, fmt.Errorf("%w: %q", ErrUnknownCapability, id)
	}
	handler, bound := r.handlers[id]
	if !bound {
		return contract, nil, fmt.Errorf("%w: %q", ErrNotImplemented, id)
	}
	return contract, handler, nil
}

// Contract devuelve solo el contrato (sin el handler) — para políticas y
// scoring, que necesitan mirar effects/requires antes de ejecutar nada.
func (r *Registry) Contract(id string) (Contract, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.contracts[id]
	return c, ok
}

// All devuelve todos los contratos registrados, ordenados por ID
// (determinista, para que un scoring o una salida de CLI no dependa del
// orden de un mapa).
func (r *Registry) All() []Contract {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Contract, 0, len(r.contracts))
	for _, c := range r.contracts {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListForTool devuelve los contratos de UNA Tool — las "rutas internas"
// que esa Tool expone (§ 8 del pedido de Experience).
func (r *Registry) ListForTool(toolID string) []Contract {
	r.mu.RLock()
	ids := append([]string(nil), r.byTool[toolID]...)
	r.mu.RUnlock()

	out := make([]Contract, 0, len(ids))
	for _, id := range ids {
		if c, ok := r.Contract(id); ok {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CheckRequirements verifica TODAS las precondiciones declaradas en el
// contrato antes de ejecutar. checker nil significa que ningún
// requirement puede verificarse: si el contrato declara alguno, falla —
// nunca se ejecuta algo cuyas precondiciones nadie pudo confirmar.
func CheckRequirements(ctx context.Context, contract Contract, checker RequirementChecker) error {
	if len(contract.Requires) == 0 {
		return nil
	}
	if checker == nil {
		return fmt.Errorf("%w: %q declara %v y no hay ningún verificador registrado",
			ErrRequirementUnsatisfied, contract.ID, contract.Requires)
	}
	for _, req := range contract.Requires {
		if !checker.Satisfied(ctx, req) {
			return fmt.Errorf("%w: %q requiere %q", ErrRequirementUnsatisfied, contract.ID, req)
		}
	}
	return nil
}

// GuaranteesSatisfied chequea que la salida cumpla lo que el contrato
// prometió. Solo entiende la forma "returns:<clave>" — cualquier otra
// garantía se considera no verificable acá y se ignora (el evaluador
// sabe cuántas pudo verificar, ver el segundo valor de retorno: no es lo
// mismo "cumplió todo" que "no había nada verificable").
func GuaranteesSatisfied(contract Contract, out Output) (satisfied bool, checked int) {
	satisfied = true
	for _, g := range contract.Guarantees {
		key, ok := strings.CutPrefix(g, "returns:")
		if !ok {
			continue
		}
		checked++
		if _, present := out[key]; !present {
			satisfied = false
		}
	}
	return satisfied, checked
}

// RequireIsolation es el chequeo explícito para una Tool que declara
// Isolation (ej. una que ejecuta código): el runtime lo llama antes de
// ejecutar, y falla si quien registró el handler no confirmó que corre
// aislado. Nunca se otorgan automáticamente los permisos del proceso
// host de Asterion a una capability de este tipo.
func RequireIsolation(contract Contract, isolatedHandlers map[string]bool) error {
	if contract.Isolation == "" {
		return nil
	}
	if !isolatedHandlers[contract.ID] {
		return fmt.Errorf(
			"tool: %q pertenece a una Tool con isolation=%q pero su handler no fue registrado como aislado — "+
				"una capability de ejecución de código nunca corre con los permisos del proceso host",
			contract.ID, contract.Isolation)
	}
	return nil
}
