package bot

import (
	"context"
	"testing"

	"github.com/Tarafagat/asterion-language/agcaspec"
	"github.com/Tarafagat/asterion-language/parser"

	"github.com/Tarafagat/asterion-graph-cognitive-architecture/runtime"
)

const testFile = `
language "0.1"

brain = AGCA.intelligence(name="Brain")
admin_bot = AGCA.bot(intelligence=brain, name="AdminBot", interface="terminal", permissions=["inventory.read"])
`

func TestNew_RejectsBotFromDifferentIntelligence(t *testing.T) {
	prog, diags := parser.Parse([]byte(testFile), "test.asterion")
	if diags.HasErrors() {
		t.Fatalf("no parsea: %s", diags.String())
	}
	spec, diags := agcaspec.Compile(prog)
	if diags.HasErrors() {
		t.Fatalf("no compila: %s", diags.String())
	}
	rt, err := runtime.Build(spec, "brain")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	if _, err := New(rt, agcaspec.BotDecl{Intelligence: "otra"}); err == nil {
		t.Fatal("esperaba un error: el bot declara pertenecer a otra intelligence")
	}

	b, err := New(rt, spec.Bots[0])
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	result, err := b.Ask(context.Background(), "quiero leer el inventario")
	if err != nil {
		t.Fatalf("Ask error: %v", err)
	}
	if result.Output != "read" {
		t.Errorf("Output = %q, want \"read\"", result.Output)
	}
}
