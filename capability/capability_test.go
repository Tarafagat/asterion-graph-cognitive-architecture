package capability

import "testing"

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
