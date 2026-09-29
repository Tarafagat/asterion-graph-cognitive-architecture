package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tarafagat/asterion-language/agcaspec"

	"github.com/Tarafagat/asterion-graph-cognitive-architecture/cognition"
	"github.com/Tarafagat/asterion-graph-cognitive-architecture/graph"
	"github.com/Tarafagat/asterion-graph-cognitive-architecture/tool"
)

// Act es el acto cognitivo completo de AGCA — el que conecta el Primer
// Principio (un World hecho de nodos) con el Segundo (la experiencia
// modifica la certeza futura). El orden de este pipeline es obligatorio
// y no se puede saltear ningún paso:
//
//	Decision -> Selected Capability -> Policy Check -> Agent Permission
//	  -> Tool Contract Validation -> Requirements Validation -> Handler
//	  -> Result -> Evaluation -> Experience -> Confidence Update
//
// Las dos verificaciones de autoridad (políticas y permisos del agente)
// ocurren DENTRO de la selección, antes de que exista siquiera una
// capability seleccionada — y se vuelven a verificar acá antes de
// resolver el handler, porque una Decision puede persistirse y
// reejecutarse, y la autoridad debe validarse contra el estado de AHORA,
// no contra el de cuando se decidió.
//
// Una Decision que no selecciona nada (sin certeza suficiente, o todo
// descartado por políticas) NO es un error: se guarda igual, con su
// motivo, y devuelve ErrNoExecution. Rechazar una acción es un resultado
// cognitivo legítimo.
func (rt *Runtime) Act(ctx context.Context, req ActRequest) (*ActResult, error) {
	started := time.Now()
	worldBefore := rt.worldSnapshot()

	goal, intentName := req.Goal, req.Intent
	if intentName == "" {
		intentName = goal
	}
	intent := cognition.Intent{Name: intentName, Goal: goal}
	key := cognition.ContextKey{World: rt.worldName(), Goal: intentName}
	goalVec := cognition.IntentVector(intent)
	worldVec := rt.worldVector()

	// Autoridad efectiva: ROL (de quién opera) compuesto con AGENTE
	// (quién actúa). Las dos tienen que permitir; cualquiera puede
	// denegar.
	role, agentVar, err := rt.resolveActor(req)
	if err != nil {
		return nil, err
	}
	agentGate, err := rt.permissionGate(agentVar)
	if err != nil {
		return nil, err
	}
	permissions := cognition.PermissionGate(&composedPermissions{role: role, agent: agentGate})

	decision := rt.Selector.Select(
		rt.Tools.All(), intent, key, goalVec, worldVec, rt.policyGate(), permissions,
	)
	decision.ID = newID("dec")
	decision.WorldStateRef = worldBefore.Ref
	if role != nil {
		decision.Role = role.Name
		decision.ReasoningTrace.Add("rol=" + role.Name + " (cadena: " + strings.Join(role.Chain, " -> ") + ")")
	}
	if req.User != "" {
		decision.User = req.User
	}

	// La Decision se persiste SIEMPRE, se haya ejecutado o no — la
	// trazabilidad no depende del resultado.
	if err := rt.Decisions.Save(ctx, decision); err != nil {
		return nil, fmt.Errorf("runtime: no pude guardar la decision: %w", err)
	}

	if !decision.Executed() {
		return &ActResult{Decision: decision}, fmt.Errorf("%w: %s", ErrNoExecution, decision.NoExecutionReason)
	}

	// Validación del contrato: el ID seleccionado DEBE resolverse contra
	// un handler registrado. Si no existe, no hay aproximación posible.
	contract, handler, err := rt.Tools.Resolve(decision.SelectedCapability)
	if err != nil {
		return &ActResult{Decision: decision}, err
	}

	// Segunda verificación de autoridad, ahora contra el estado actual.
	if allowed, reason := permissions.Permitted(contract.ID); !allowed {
		return &ActResult{Decision: decision}, fmt.Errorf("%w: %s", ErrPermissionDenied, reason)
	}
	if allowed, policy, reason := rt.policyGate().Check(contract, intent); !allowed {
		return &ActResult{Decision: decision}, fmt.Errorf("%w: política %q: %s", ErrPolicyDenied, policy, reason)
	}

	// Aislamiento: una Tool que ejecuta código nunca corre con los
	// permisos del proceso host solo porque fue seleccionada.
	if err := tool.RequireIsolation(contract, rt.isolatedHandlers); err != nil {
		return &ActResult{Decision: decision}, err
	}

	// Precondiciones declaradas en el contrato.
	if err := tool.CheckRequirements(ctx, contract, rt.Requirements); err != nil {
		return &ActResult{Decision: decision}, err
	}

	// Recién acá se ejecuta.
	result, execErr := handler.Execute(ctx, rt.inputFor(contract, goal))
	if execErr != nil && result.Err == nil {
		result.Err = execErr
	}
	result.CapabilityID = contract.ID

	worldAfter := rt.recordAct(decision, contract, result)

	evaluation := rt.Evaluator.Evaluate(ctx, decision, contract, result, worldBefore, worldAfter)
	previous, updated := rt.Learner.Learn(ctx, rt.Confidence, contract.ID, key, evaluation)

	exp := cognition.Experience{
		ID:                 newID("exp"),
		DecisionID:         decision.ID,
		WorldBeforeRef:     worldBefore.Ref,
		WorldAfterRef:      worldAfter.Ref,
		SelectedCapability: contract.ID,
		Succeeded:          result.Succeeded(),
		DurationMS:         time.Since(started).Milliseconds(),
		Evaluation:         evaluation,
		Context:            key,
		ContextVector:      mergeVectors(goalVec, worldVec),
		PreviousConfidence: previous,
		ResultConfidence:   updated,
		ConfidenceDelta:    updated - previous,
		CreatedAt:          time.Now().UTC(),
	}
	if result.Err != nil {
		exp.ErrorMessage = result.Err.Error()
	}
	if err := rt.Experiences.SaveExperience(ctx, exp); err != nil {
		return nil, fmt.Errorf("runtime: no pude guardar la experience: %w", err)
	}
	if rt.persist != nil {
		if err := rt.persist.SaveConfidence(rt.Confidence); err != nil {
			return nil, fmt.Errorf("runtime: no pude persistir la certeza aprendida: %w", err)
		}
	}

	return &ActResult{Decision: decision, Contract: contract, Result: result, Experience: exp}, nil
}

// ActRequest es lo que se le pide a un acto cognitivo. Role/User son la
// autoridad de QUIÉN opera (un viewer y un admin no pueden lo mismo);
// Agent es quién actúa dentro de la inteligencia. Si se dan los dos, la
// autoridad efectiva es la intersección.
//
// User resuelve su rol contra los users=[...] declarados en
// AGCA.role(...) — así el CLI no necesita que nadie declare su propio
// rol a mano en cada invocación.
type ActRequest struct {
	Goal   string
	Intent string
	Agent  string
	Role   string
	User   string
}

// resolveActor decide qué rol y qué agente aplican. Sin rol NI usuario,
// se cae al rol del propio agente si lo declaró — nunca a "sin
// restricciones".
func (rt *Runtime) resolveActor(req ActRequest) (*Role, string, error) {
	agentVar := req.Agent

	switch {
	case req.Role != "" && req.User != "":
		return nil, "", fmt.Errorf("runtime: indicá --role o --user, no los dos (el usuario ya determina su rol)")
	case req.Role != "":
		role, err := rt.ResolveRole(req.Role)
		if err != nil {
			return nil, "", err
		}
		return &role, agentVar, nil
	case req.User != "":
		role, err := rt.RoleForUser(req.User)
		if err != nil {
			return nil, "", err
		}
		return &role, agentVar, nil
	}

	// Sin rol explícito: si el agente declaró uno, ese aplica.
	if agentVar != "" {
		for _, a := range rt.Agents {
			if a.VarName == agentVar && a.Role != "" {
				role, err := rt.ResolveRole(a.Role)
				if err != nil {
					return nil, "", err
				}
				return &role, agentVar, nil
			}
		}
	}
	// Sin rol declarado por ningún lado: la autoridad queda solo en el
	// agente. Si tampoco hay agente, permissionGate usa la unión de
	// todos — que sigue siendo deny-by-default si ninguno declaró allow.
	return nil, agentVar, nil
}

// ActResult es todo lo que produjo un acto cognitivo — la Decision
// (siempre), y el resto cuando hubo ejecución.
type ActResult struct {
	Decision   cognition.Decision
	Contract   tool.Contract
	Result     tool.ExecutionResult
	Experience cognition.Experience
}

// Errores del pipeline, distinguibles por el caller (el CLI los explica
// distinto: "no ejecuté por falta de certeza" no es lo mismo que "te lo
// negó una política").
var (
	ErrNoExecution      = fmt.Errorf("runtime: NO_EXECUTION")
	ErrPolicyDenied     = fmt.Errorf("runtime: denegado por política")
	ErrPermissionDenied = fmt.Errorf("runtime: denegado por permisos del agente")
)

// recordAct deja el rastro del acto en el Cognitive Graph: un nodo de
// acción con su procedencia, encadenado a la decisión que lo originó.
func (rt *Runtime) recordAct(d cognition.Decision, c tool.Contract, result tool.ExecutionResult) cognition.WorldSnapshot {
	prov := graph.Provenance{Source: "capability:" + c.ID, TraceID: d.ID}
	kind := "action"
	if !result.Succeeded() {
		kind = "action_failed"
	}
	rt.Graph.AddNode(graph.Node{
		ID:         d.ID + ":action",
		Kind:       kind,
		Payload:    result.Output,
		Confidence: d.Confidence,
		Provenance: prov,
	})
	return rt.worldSnapshot()
}

func (rt *Runtime) worldSnapshot() cognition.WorldSnapshot {
	return cognition.WorldSnapshot{Ref: fmt.Sprintf("%s@%d", rt.worldName(), rt.Graph.Len()), NodeCount: rt.Graph.Len()}
}

func (rt *Runtime) worldName() string {
	if rt.GraphSpec != nil && rt.GraphSpec.Name != "" {
		return rt.GraphSpec.Name
	}
	return rt.Intelligence.Name
}

// worldVector describe el estado del World como features explícitas —
// hoy, qué tipos de nodo contiene y en qué proporción. Sin embeddings:
// es el propio grafo describiéndose a sí mismo.
func (rt *Runtime) worldVector() cognition.Vector {
	v := cognition.Vector{}
	if name := rt.worldName(); name != "" {
		v["world."+strings.ToLower(name)] = 1
	}
	for _, kind := range []string{"observation", "insight", "action", "action_failed"} {
		if n := len(rt.Graph.ByKind(kind)); n > 0 {
			v["node."+kind] = 1
		}
	}
	for _, c := range rt.RequiredCaps {
		v["requires."+c.Capability] = 0.5
	}
	return v
}

// inputFor arma la entrada del handler a partir del goal. Es
// deliberadamente pobre: pasa el goal como dato, NUNCA como algo a
// interpretar o ejecutar. Un planner real (Milestone 6) llenaría los
// campos declarados en contract.Input; hoy se pasa el goal crudo bajo
// "goal" y el handler decide qué hacer con él — dentro de su contrato.
func (rt *Runtime) inputFor(c tool.Contract, goal string) tool.Input {
	in := tool.Input{"goal": goal}
	for _, field := range c.Input {
		name, _, _ := strings.Cut(field, ":")
		if name != "" && name != "goal" {
			in[name] = goal
		}
	}
	return in
}

// policyGate traduce los AGCA.policy(...) declarados a un gate real.
func (rt *Runtime) policyGate() cognition.PolicyGate { return &declaredPolicies{policies: rt.Policies} }

// declaredPolicies evalúa las políticas declarativas del .asterion. Solo
// entiende la forma que hoy puede verificar de verdad — efectos del
// contrato ("privacy==local" no aplica a Tools; "read_only"/"mutating" sí)
// — y NUNCA inventa una interpretación para una condición que no
// entiende: una política incomprensible no bloquea ni autoriza en
// silencio, queda registrada como no evaluada.
type declaredPolicies struct {
	policies []agcaspec.PolicyDecl
}

func (p *declaredPolicies) Check(contract tool.Contract, _ cognition.Intent) (bool, string, string) {
	for _, pol := range p.policies {
		// deny="mutating" / deny="destructive": el efecto declarado por el
		// contrato alcanza para decidir ANTES de ejecutar.
		if pol.Deny != "" && contract.HasEffect(pol.Deny) {
			return false, pol.Name, fmt.Sprintf("la política %q deniega el efecto %q", pol.Name, pol.Deny)
		}
		if pol.Deny == "mutating" && contract.IsMutating() {
			return false, pol.Name, fmt.Sprintf("la política %q deniega capabilities mutantes", pol.Name)
		}
		// allow="read_only": si una política SOLO permite read_only,
		// cualquier capability mutante queda fuera.
		if pol.Allow == tool.EffectReadOnly && contract.IsMutating() {
			return false, pol.Name, fmt.Sprintf("la política %q solo permite capabilities read_only", pol.Name)
		}
	}
	return true, "", ""
}

// permissionGate arma la frontera de autoridad del agente indicado. Sin
// agente, se usa la unión de lo que TODOS los agentes de esta
// Intelligence permiten — y si ninguno declara allow, no hay autoridad
// para nada (deny-by-default).
func (rt *Runtime) permissionGate(agentVar string) (cognition.PermissionGate, error) {
	if agentVar == "" {
		var allow, deny []string
		for _, a := range rt.Agents {
			allow = append(allow, a.Allow...)
			deny = append(deny, a.Deny...)
		}
		return &agentPermissions{agent: "(todos)", allow: allow, deny: deny}, nil
	}
	for _, a := range rt.Agents {
		if a.VarName == agentVar {
			return &agentPermissions{agent: a.Name, allow: a.Allow, deny: a.Deny}, nil
		}
	}
	return nil, fmt.Errorf("runtime: esta intelligence no declara ningún AGCA.agent(...) llamado %q", agentVar)
}

// agentPermissions implementa "Tool instalada ≠ Tool accesible": deny
// gana siempre; sin allow explícito no hay autoridad.
type agentPermissions struct {
	agent string
	allow []string
	deny  []string
}

func (p *agentPermissions) Permitted(capabilityID string) (bool, string) {
	for _, d := range p.deny {
		if d == capabilityID {
			return false, fmt.Sprintf("el agente %q deniega explícitamente %q", p.agent, capabilityID)
		}
	}
	for _, a := range p.allow {
		if a == capabilityID {
			return true, ""
		}
	}
	return false, fmt.Sprintf("el agente %q no tiene %q en su lista allow (deny-by-default)", p.agent, capabilityID)
}

func mergeVectors(a, b cognition.Vector) cognition.Vector {
	out := cognition.Vector{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if existing, ok := out[k]; !ok || v > existing {
			out[k] = v
		}
	}
	return out
}

var idCounter int64

func newID(prefix string) string {
	idCounter++
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), idCounter)
}
