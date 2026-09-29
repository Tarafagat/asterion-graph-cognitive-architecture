package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/Tarafagat/asterion-language/agcaspec"
	"github.com/Tarafagat/asterion-language/parser"

	"github.com/Tarafagat/asterion-graph-cognitive-architecture/tool"
)

const roleFile = `
language "0.1"

stats = Tool.define(name="Statistics", category="statistics")
search = Tool.capability(tool=stats, name="search_series", effects=["read_only"], guarantees=["returns:series"])
mean = Tool.capability(tool=stats, name="calculate_mean", effects=["pure"], guarantees=["returns:mean"])

inv = Tool.define(name="Inventory", category="inventory")
get_stock = Tool.capability(tool=inv, name="get_stock", effects=["read_only"], guarantees=["returns:stock"])
delete_record = Tool.capability(tool=inv, name="delete_record", effects=["destructive"])

brain = AGCA.intelligence(name="Brain")
world = AGCA.graph(intelligence=brain, name="EconomicResearch")

viewer = AGCA.role(
    intelligence=brain,
    name="viewer",
    description="Solo lectura estadística",
    allow=["statistics.search_series"],
    users=["lector@example.com"],
)

analyst = AGCA.role(
    intelligence=brain,
    name="analyst",
    inherits=[viewer],
    allow=["statistics.calculate_mean", "inventory.get_stock"],
    users=["analista@example.com"],
)

admin = AGCA.role(
    intelligence=brain,
    name="admin",
    inherits=[analyst],
    allow=["inventory.delete_record"],
    deny=["statistics.calculate_mean"],
    users=["jefe@example.com"],
)

worker = AGCA.agent(
    intelligence=brain,
    name="Worker",
    allow=["statistics.search_series", "statistics.calculate_mean", "inventory.get_stock", "inventory.delete_record"],
)
`

func buildRoleRuntime(t *testing.T) *Runtime {
	t.Helper()
	prog, diags := parser.Parse([]byte(roleFile), "roles.asterion")
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

// Heredar AMPLÍA lo permitido: analyst suma lo suyo a lo del viewer.
func TestResolveRole_InheritanceAccumulatesAllow(t *testing.T) {
	rt := buildRoleRuntime(t)

	viewer, err := rt.ResolveRole("viewer")
	if err != nil {
		t.Fatalf("ResolveRole(viewer): %v", err)
	}
	if len(viewer.Allow) != 1 || viewer.Allow[0] != "statistics.search_series" {
		t.Fatalf("viewer.Allow = %v, want solo search_series", viewer.Allow)
	}

	analyst, err := rt.ResolveRole("analyst")
	if err != nil {
		t.Fatalf("ResolveRole(analyst): %v", err)
	}
	for _, want := range []string{"statistics.search_series", "statistics.calculate_mean", "inventory.get_stock"} {
		if allowed, _ := analyst.Allows(want); !allowed {
			t.Errorf("analyst debería poder %q (allow efectivo: %v)", want, analyst.Allow)
		}
	}
	// Lo que nadie le dio, sigue negado.
	if allowed, reason := analyst.Allows("inventory.delete_record"); allowed {
		t.Error("analyst NO debería poder borrar registros")
	} else if reason == "" {
		t.Error("un deny siempre tiene que venir con motivo")
	}
	if strings.Join(analyst.Chain, ",") != "viewer,analyst" {
		t.Errorf("Chain = %v, want [viewer analyst] (raíz primero)", analyst.Chain)
	}
}

// DENY GANA SIEMPRE: admin hereda calculate_mean de analyst, pero lo
// deniega explícitamente — heredar no puede reabrir algo prohibido.
func TestResolveRole_DenyBeatsInheritedAllow(t *testing.T) {
	rt := buildRoleRuntime(t)
	admin, err := rt.ResolveRole("admin")
	if err != nil {
		t.Fatalf("ResolveRole(admin): %v", err)
	}

	if allowed, _ := admin.Allows("statistics.calculate_mean"); allowed {
		t.Errorf("admin deniega calculate_mean explícitamente; el allow heredado no puede reabrirlo (allow=%v deny=%v)",
			admin.Allow, admin.Deny)
	}
	for _, s := range admin.Allow {
		if s == "statistics.calculate_mean" {
			t.Error("una capability denegada no debería quedar en el allow efectivo")
		}
	}
	// Lo demás de la cadena sí lo tiene.
	if allowed, _ := admin.Allows("statistics.search_series"); !allowed {
		t.Error("admin debería heredar search_series de viewer")
	}
	if allowed, _ := admin.Allows("inventory.delete_record"); !allowed {
		t.Error("admin debería poder borrar registros (allow propio)")
	}
}

func TestResolveRole_CycleIsRejected(t *testing.T) {
	// a hereda de b, y b de a: el lenguaje lo deja declarar (b se declara
	// antes), pero resolverlo es imposible y tiene que fallar explícito.
	src := `
language "0.1"
brain = AGCA.intelligence(name="Brain")
b = AGCA.role(intelligence=brain, name="b")
a = AGCA.role(intelligence=brain, name="a", inherits=[b])
`
	prog, _ := parser.Parse([]byte(src), "cycle.asterion")
	spec, diags := agcaspec.Compile(prog, "")
	if diags.HasErrors() {
		t.Fatalf("no compila: %s", diags.String())
	}
	// Se fuerza el ciclo a mano (el compilador no puede declararlo por la
	// regla de declarar-antes-de-usar, pero el runtime igual debe
	// defenderse de un spec armado programáticamente).
	for i := range spec.Roles {
		if spec.Roles[i].VarName == "b" {
			spec.Roles[i].Inherits = []string{"a"}
		}
	}
	rt, err := Build(spec, "brain")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := rt.ResolveRole("a"); err == nil || !strings.Contains(err.Error(), "ciclo") {
		t.Fatalf("err = %v, want un error de ciclo de herencia", err)
	}
}

func TestRoleForUser(t *testing.T) {
	rt := buildRoleRuntime(t)

	role, err := rt.RoleForUser("analista@example.com")
	if err != nil {
		t.Fatalf("RoleForUser: %v", err)
	}
	if role.Name != "analyst" {
		t.Errorf("rol = %q, want analyst", role.Name)
	}

	// Un usuario sin rol no tiene autoridad — y se dice, no se asume.
	if _, err := rt.RoleForUser("desconocido@example.com"); err == nil {
		t.Error("un usuario sin rol declarado debería dar error, no permisos vacíos silenciosos")
	}
}

// La autoridad efectiva es la INTERSECCIÓN: el agente puede todo, pero
// el rol del usuario recorta lo que de verdad se puede hacer.
func TestAct_RoleRestrictsBeyondAgent(t *testing.T) {
	rt := buildRoleRuntime(t)
	deleted := false
	_ = rt.BindHandler("inventory.delete_record", tool.HandlerFunc(func(_ context.Context, _ tool.Input) (tool.ExecutionResult, error) {
		deleted = true
		return tool.ExecutionResult{Output: tool.Output{"ok": true}}, nil
	}), false)

	// El agente "Worker" tiene delete_record en su allow, pero el rol
	// viewer no: no se ejecuta.
	out, err := rt.Act(context.Background(), ActRequest{
		Goal: "borrar el registro del inventario", Intent: "delete_record", Agent: "worker", Role: "viewer",
	})
	if err == nil {
		t.Fatal("el rol viewer no debería poder borrar registros")
	}
	if deleted {
		t.Fatal("el handler NUNCA debería invocarse: el rol no lo autoriza")
	}
	if out.Decision.Role != "viewer" {
		t.Errorf("la Decision debería registrar el rol activo, tiene %q", out.Decision.Role)
	}
	// Y el motivo del descarte tiene que nombrar al rol.
	var sawRoleReason bool
	for _, c := range out.Decision.CandidateCapabilities {
		if c.CapabilityID == "inventory.delete_record" && strings.Contains(c.RejectedReason, "viewer") {
			sawRoleReason = true
		}
	}
	if !sawRoleReason {
		t.Error("el descarte debería explicar que fue el rol quien lo negó")
	}

	// Con el rol admin, el mismo agente y el mismo goal sí ejecutan.
	if _, err := rt.Act(context.Background(), ActRequest{
		Goal: "borrar el registro del inventario", Intent: "delete_record", Agent: "worker", Role: "admin",
	}); err != nil {
		t.Fatalf("admin debería poder borrar: %v", err)
	}
	if !deleted {
		t.Fatal("con rol admin el handler debería haberse invocado")
	}
}

// El mismo goal, el mismo agente, distinto usuario -> distinta autoridad.
func TestAct_UserDeterminesAuthority(t *testing.T) {
	rt := buildRoleRuntime(t)
	_ = rt.BindHandler("inventory.get_stock", tool.HandlerFunc(func(_ context.Context, _ tool.Input) (tool.ExecutionResult, error) {
		return tool.ExecutionResult{Output: tool.Output{"stock": 42}}, nil
	}), false)

	// lector@ es viewer: no tiene inventory.get_stock.
	if _, err := rt.Act(context.Background(), ActRequest{
		Goal: "consultar el stock", Intent: "get_stock", Agent: "worker", User: "lector@example.com",
	}); err == nil {
		t.Error("un viewer no debería poder consultar stock")
	}

	// analista@ sí (lo hereda... no: lo tiene propio).
	out, err := rt.Act(context.Background(), ActRequest{
		Goal: "consultar el stock", Intent: "get_stock", Agent: "worker", User: "analista@example.com",
	})
	if err != nil {
		t.Fatalf("un analyst debería poder consultar stock: %v", err)
	}
	if out.Decision.User != "analista@example.com" || out.Decision.Role != "analyst" {
		t.Errorf("la Decision debería registrar usuario y rol: user=%q role=%q", out.Decision.User, out.Decision.Role)
	}
}

func TestAct_RoleAndUserAreMutuallyExclusive(t *testing.T) {
	rt := buildRoleRuntime(t)
	_, err := rt.Act(context.Background(), ActRequest{
		Goal: "x", Intent: "y", Role: "admin", User: "jefe@example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "no los dos") {
		t.Fatalf("err = %v, want un error por pasar --role y --user a la vez", err)
	}
}

func TestRoles_ListsEveryRoleResolved(t *testing.T) {
	rt := buildRoleRuntime(t)
	roles, err := rt.Roles()
	if err != nil {
		t.Fatalf("Roles: %v", err)
	}
	if len(roles) != 3 {
		t.Fatalf("roles = %d, want 3", len(roles))
	}
	byName := map[string]Role{}
	for _, r := range roles {
		byName[r.Name] = r
	}
	if len(byName["viewer"].Allow) >= len(byName["analyst"].Allow) {
		t.Errorf("analyst debería tener más capabilities que viewer: viewer=%v analyst=%v",
			byName["viewer"].Allow, byName["analyst"].Allow)
	}
	if byName["viewer"].Description == "" {
		t.Error("la descripción declarada debería conservarse")
	}
}
