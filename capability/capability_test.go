package capability

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Tarafagat/asterion-plugin-contract/apc"
)

func TestMatch_ReturnsOnlyDeclaredCapability(t *testing.T) {
	r := NewRegistry()
	r.Register(Provider{Name: "mock:database", Capabilities: []string{"database.query"}})
	r.Register(Provider{Name: "mock:mail", Capabilities: []string{"mail.read"}})

	got := r.Match("database.query")
	if len(got) != 1 || got[0].Name != "mock:database" {
		t.Fatalf("Match(database.query) = %+v", got)
	}
}

func TestMatch_NoneRegisteredIsEmptyNotError(t *testing.T) {
	r := NewRegistry()
	got := r.Match("nonexistent.capability")
	if len(got) != 0 {
		t.Fatalf("Match = %+v, want empty", got)
	}
}

func TestDeriveFromManifest_ResourcesAndActions(t *testing.T) {
	m := apc.Manifest{
		Name:    "asterion-sii",
		Version: "1.0.0",
		Resources: []apc.ResourceSpec{
			{Name: "invoices", Endpoint: "/invoices", CRUD: []string{"create", "read", "list"}},
		},
		Actions: []apc.ActionSpec{
			{Name: "issue_invoice", Method: "POST", Endpoint: "/invoices/{id}/issue"},
		},
	}
	p := DeriveFromManifest("sii", m)
	if p.Name != "sii" {
		t.Errorf("Name = %q, want \"sii\"", p.Name)
	}
	want := []string{"invoices.create", "invoices.read", "invoices.list", "issue_invoice"}
	if len(p.Capabilities) != len(want) {
		t.Fatalf("Capabilities = %v, want %v", p.Capabilities, want)
	}
	for i, w := range want {
		if p.Capabilities[i] != w {
			t.Errorf("Capabilities[%d] = %q, want %q", i, p.Capabilities[i], w)
		}
	}
}

func TestDeriveFromManifest_NoResourcesOrActionsIsEmpty(t *testing.T) {
	p := DeriveFromManifest("bare", apc.Manifest{Name: "bare", Version: "1.0.0"})
	if len(p.Capabilities) != 0 {
		t.Errorf("Capabilities = %v, want empty", p.Capabilities)
	}
}

func writeMinimalPlugin(t *testing.T, dir string) {
	t.Helper()
	content := `name: tutorial-db-plugin
version: "1.0.0"
start:
  command: ./tutorial-db-plugin
port: 0
resources:
  - name: invoices
    endpoint: /invoices
    crud: [create, read, list]
actions:
  - name: issue_invoice
    method: POST
    endpoint: /invoices/{id}/issue
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("no pude escribir plugin.yaml: %v", err)
	}
}

func TestLoadProviderFromLocalPlugin_Real(t *testing.T) {
	dir := t.TempDir()
	writeMinimalPlugin(t, dir)

	p, err := LoadProviderFromLocalPlugin("db", dir)
	if err != nil {
		t.Fatalf("LoadProviderFromLocalPlugin error: %v", err)
	}
	if p.Name != "db" {
		t.Errorf("Name = %q, want \"db\"", p.Name)
	}
	if len(p.Capabilities) != 4 {
		t.Fatalf("Capabilities = %v, want 4 entries", p.Capabilities)
	}
}

func TestLoadProviderFromLocalPlugin_MissingManifest(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadProviderFromLocalPlugin("db", dir); err == nil {
		t.Fatal("esperaba un error: no hay plugin.yaml en dir")
	}
}
