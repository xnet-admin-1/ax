package engine

import (
	"encoding/json"
	"testing"
)

func resolveOrder(stages []Stage) [][]string {
	completed := map[string]bool{}
	var waves [][]string
	for i := 0; i < len(stages)+1; i++ {
		var ready []string
		for _, s := range stages {
			if completed[s.Name] { continue }
			ok := true
			for _, d := range s.DependsOn { if !completed[d] { ok = false; break } }
			if ok { ready = append(ready, s.Name) }
		}
		if len(ready) == 0 { break }
		waves = append(waves, ready)
		for _, n := range ready { completed[n] = true }
	}
	return waves
}

func TestNoDeps(t *testing.T) {
	waves := resolveOrder([]Stage{{Name: "A"}, {Name: "B"}, {Name: "C"}})
	if len(waves) != 1 || len(waves[0]) != 3 { t.Fatalf("expected 1 wave of 3, got %v", waves) }
}

func TestChain(t *testing.T) {
	waves := resolveOrder([]Stage{{Name: "A"}, {Name: "B", DependsOn: []string{"A"}}, {Name: "C", DependsOn: []string{"B"}}})
	if len(waves) != 3 { t.Fatalf("expected 3 waves, got %v", waves) }
}

func TestFanOutIn(t *testing.T) {
	waves := resolveOrder([]Stage{{Name: "A"}, {Name: "B"}, {Name: "C", DependsOn: []string{"A", "B"}}})
	if len(waves) != 2 || len(waves[0]) != 2 { t.Fatalf("expected [[A,B],[C]], got %v", waves) }
}

func TestCircularDeadlock(t *testing.T) {
	waves := resolveOrder([]Stage{{Name: "A", DependsOn: []string{"B"}}, {Name: "B", DependsOn: []string{"A"}}})
	if len(waves) != 0 { t.Fatalf("circular deps should yield no waves, got %v", waves) }
}

func TestStageJSON(t *testing.T) {
	s := Stage{Name: "x", Agent: "coder", Prompt: "do it", DependsOn: []string{"a"}}
	b, _ := json.Marshal(s)
	var out Stage
	json.Unmarshal(b, &out)
	if out.Name != "x" || out.DependsOn[0] != "a" { t.Fatal("json round-trip failed") }
}
