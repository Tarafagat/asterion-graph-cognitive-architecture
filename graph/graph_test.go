package graph

import "testing"

func TestAddNode_VersionsIncrement(t *testing.T) {
	s := NewStore()
	n1 := s.AddNode(Node{ID: "a", Kind: "observation"})
	if n1.Version != 1 {
		t.Fatalf("Version = %d, want 1", n1.Version)
	}
	n2 := s.AddNode(Node{ID: "a", Kind: "observation"})
	if n2.Version != 2 {
		t.Fatalf("Version = %d, want 2 (segunda escritura del mismo ID)", n2.Version)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (mismo ID, no un nodo nuevo)", s.Len())
	}
}

func TestAddEdge_RequiresExistingNodes(t *testing.T) {
	s := NewStore()
	s.AddNode(Node{ID: "a"})
	if err := s.AddEdge(Edge{From: "a", To: "nonexistent", Kind: "derived_from"}); err == nil {
		t.Fatal("esperaba un error: 'To' no existe")
	}
	s.AddNode(Node{ID: "b"})
	if err := s.AddEdge(Edge{From: "a", To: "b", Kind: "derived_from"}); err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	edges := s.EdgesFrom("a")
	if len(edges) != 1 || edges[0].To != "b" {
		t.Fatalf("EdgesFrom(a) = %+v", edges)
	}
}

func TestByKind(t *testing.T) {
	s := NewStore()
	s.AddNode(Node{ID: "a", Kind: "observation"})
	s.AddNode(Node{ID: "b", Kind: "insight"})
	s.AddNode(Node{ID: "c", Kind: "observation"})
	obs := s.ByKind("observation")
	if len(obs) != 2 {
		t.Fatalf("ByKind(observation) = %d nodos, want 2", len(obs))
	}
}

func TestSnapshot_IsFrozen(t *testing.T) {
	s := NewStore()
	s.AddNode(Node{ID: "a", Kind: "observation"})
	snap := s.Snapshot()
	if len(snap.Nodes) != 1 {
		t.Fatalf("Snapshot.Nodes = %d, want 1", len(snap.Nodes))
	}
	s.AddNode(Node{ID: "b", Kind: "observation"})
	if len(snap.Nodes) != 1 {
		t.Fatalf("Snapshot debería seguir teniendo 1 nodo tras una escritura posterior al store, tiene %d", len(snap.Nodes))
	}
	if s.Len() != 2 {
		t.Fatalf("Store.Len() = %d, want 2", s.Len())
	}
}
