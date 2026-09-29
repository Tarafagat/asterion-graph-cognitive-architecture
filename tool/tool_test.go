package tool

import (
	"context"
	"errors"
	"testing"
)

func statsContract(name string, effects ...string) Contract {
	return Contract{ID: "statistics." + name, Tool: "Statistics", Name: name, Category: "statistics", Effects: effects}
}

func okHandler() Handler {
	return HandlerFunc(func(_ context.Context, _ Input) (ExecutionResult, error) {
		return ExecutionResult{Output: Output{"series": []int{1, 2, 3}}}, nil
	})
}

// El requisito central del pedido: una Tool SOLO puede ejecutar las
// capabilities que fueron registradas explícitamente.
func TestRegistry_OnlyRegisteredCapabilitiesResolve(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(statsContract("search_series", EffectReadOnly), okHandler()); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, _, err := r.Resolve("statistics.search_series"); err != nil {
		t.Fatalf("una capability registrada debería resolverse: %v", err)
	}

	_, _, err := r.Resolve("statistics.no_existe")
	if !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("err = %v, want ErrUnknownCapability", err)
	}
}

// Una Tool de estadística NO puede ejecutar SQL arbitrario ni shell:
// esos IDs simplemente no existen salvo que alguien los declare.
func TestRegistry_StatisticsToolCannotExecuteArbitraryCode(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(statsContract("search_series", EffectReadOnly), okHandler())
	_ = r.Register(statsContract("calculate_mean", EffectPure), okHandler())

	for _, forbidden := range []string{
		"statistics.raw_sql",
		"statistics.execute_sql",
		"statistics.shell",
		"statistics.execute",
		"statistics.eval",
		"database.raw_sql",
		"system.shell",
	} {
		if _, _, err := r.Resolve(forbidden); !errors.Is(err, ErrUnknownCapability) {
			t.Errorf("Resolve(%q) = %v, want ErrUnknownCapability — no debe existir ninguna ruta hacia ejecución arbitraria", forbidden, err)
		}
	}

	// Y lo que SÍ declaró sigue siendo exactamente eso, nada más.
	if got := len(r.ListForTool("Statistics")); got != 2 {
		t.Errorf("ListForTool = %d capabilities, want 2 (solo las declaradas)", got)
	}
}

// Una capability declarada en un .asterion pero sin handler atado es
// visible pero NO ejecutable — distinto de "no existe".
func TestRegistry_DeclaredWithoutHandlerIsNotExecutable(t *testing.T) {
	r := NewRegistry()
	if err := r.Declare(statsContract("correlation", EffectReadOnly)); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	if r.Implemented("statistics.correlation") {
		t.Fatal("una capability declarada sin Bind no debería figurar como implementada")
	}

	_, _, err := r.Resolve("statistics.correlation")
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
	if errors.Is(err, ErrUnknownCapability) {
		t.Fatal("no debería confundirse con una capability inexistente: el contrato existe")
	}

	if err := r.Bind("statistics.correlation", okHandler()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if _, _, err := r.Resolve("statistics.correlation"); err != nil {
		t.Fatalf("tras Bind debería resolverse: %v", err)
	}
}

func TestRegistry_CannotBindUndeclaredOrRebind(t *testing.T) {
	r := NewRegistry()
	if err := r.Bind("statistics.fantasma", okHandler()); !errors.Is(err, ErrUnknownCapability) {
		t.Errorf("Bind sobre algo no declarado = %v, want ErrUnknownCapability", err)
	}

	_ = r.Declare(statsContract("mean", EffectPure))
	_ = r.Bind("statistics.mean", okHandler())
	if err := r.Bind("statistics.mean", okHandler()); err == nil {
		t.Error("no debería poder reemplazarse el handler de una capability ya atada")
	}
}

func TestRegistry_RegisterRejectsNilHandlerAndDuplicates(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(statsContract("x"), nil); err == nil {
		t.Error("un handler nil debería rechazarse")
	}
	if err := r.Register(Contract{}, okHandler()); err == nil {
		t.Error("un contrato sin ID debería rechazarse")
	}
	_ = r.Register(statsContract("y"), okHandler())
	if err := r.Register(statsContract("y"), okHandler()); err == nil {
		t.Error("registrar dos veces el mismo ID debería rechazarse")
	}
}

func TestContract_EffectsQueries(t *testing.T) {
	ro := statsContract("search", EffectReadOnly)
	if !ro.IsReadOnly() || ro.IsMutating() {
		t.Errorf("read_only mal clasificado: %+v", ro)
	}
	del := Contract{ID: "db.delete", Effects: []string{EffectDestructive, "financial_operation"}}
	if del.IsReadOnly() || !del.IsMutating() {
		t.Errorf("destructive mal clasificado: %+v", del)
	}
	if !del.HasEffect("financial_operation") {
		t.Error("HasEffect debería ver también los tags libres")
	}
}

func TestCheckRequirements(t *testing.T) {
	c := Contract{ID: "pay.create", Requires: []string{"terminal.online"}}

	// Sin verificador, un contrato con requirements NO se ejecuta.
	if err := CheckRequirements(context.Background(), c, nil); !errors.Is(err, ErrRequirementUnsatisfied) {
		t.Errorf("err = %v, want ErrRequirementUnsatisfied (sin verificador no se asume nada)", err)
	}

	offline := RequirementFunc(func(context.Context, string) bool { return false })
	if err := CheckRequirements(context.Background(), c, offline); !errors.Is(err, ErrRequirementUnsatisfied) {
		t.Errorf("err = %v, want ErrRequirementUnsatisfied", err)
	}

	online := RequirementFunc(func(_ context.Context, req string) bool { return req == "terminal.online" })
	if err := CheckRequirements(context.Background(), c, online); err != nil {
		t.Errorf("con el requirement satisfecho no debería fallar: %v", err)
	}

	// Un contrato sin requirements no necesita verificador.
	if err := CheckRequirements(context.Background(), Contract{ID: "x"}, nil); err != nil {
		t.Errorf("sin requirements no debería fallar: %v", err)
	}
}

func TestGuaranteesSatisfied(t *testing.T) {
	c := Contract{ID: "s.search", Guarantees: []string{"returns:series", "es_rapido"}}

	ok, checked := GuaranteesSatisfied(c, Output{"series": 1})
	if !ok || checked != 1 {
		t.Errorf("ok=%v checked=%d, want true/1 (solo 'returns:' es verificable)", ok, checked)
	}
	ok, checked = GuaranteesSatisfied(c, Output{"otra_cosa": 1})
	if ok || checked != 1 {
		t.Errorf("ok=%v checked=%d, want false/1", ok, checked)
	}
	_, checked = GuaranteesSatisfied(Contract{Guarantees: []string{"es_rapido"}}, Output{})
	if checked != 0 {
		t.Errorf("checked=%d, want 0 (nada verificable)", checked)
	}
}

func TestRequireIsolation(t *testing.T) {
	sandboxed := Contract{ID: "sandboxedpython.execute_python", Isolation: "sandbox"}

	if err := RequireIsolation(sandboxed, map[string]bool{}); err == nil {
		t.Error("una capability con isolation y handler no confirmado como aislado debería rechazarse")
	}
	if err := RequireIsolation(sandboxed, map[string]bool{"sandboxedpython.execute_python": true}); err != nil {
		t.Errorf("con el handler confirmado aislado no debería fallar: %v", err)
	}
	if err := RequireIsolation(Contract{ID: "statistics.mean"}, map[string]bool{}); err != nil {
		t.Errorf("una capability sin isolation no necesita confirmación: %v", err)
	}
}
