package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukashankeln/glint/internal/config"
)

func writeRuleFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadRuleFiles_MalformedListIsError(t *testing.T) {
	cases := map[string]string{
		"type error in rule": "rules:\n  - id: a\n    expression: \"true\"\n    match: {kinds: Deployment}\n",
		"rules is mapping":   "rules:\n  id: a\n  expression: \"true\"\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeRuleFile(t, content)
			_, err := LoadRuleFiles([]string{p})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), p) {
				t.Errorf("error %q does not mention path %q", err, p)
			}
		})
	}
}

func TestLoadRuleFiles_ValidFiles(t *testing.T) {
	list := writeRuleFile(t, "rules:\n  - id: a\n    expression: \"true\"\n  - id: b\n    expression: \"true\"\n")
	got, err := LoadRuleFiles([]string{list})
	if err != nil || len(got) != 2 {
		t.Fatalf("list: got %d rules, err %v", len(got), err)
	}
	single := writeRuleFile(t, "id: solo\nexpression: \"true\"\n")
	got, err = LoadRuleFiles([]string{single})
	if err != nil || len(got) != 1 || got[0].ID != "solo" {
		t.Fatalf("single: got %+v, err %v", got, err)
	}
}

func TestLoadRuleFiles_EmptyFile(t *testing.T) {
	p := writeRuleFile(t, "")
	got, err := LoadRuleFiles([]string{p})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %d rules, err %v", len(got), err)
	}
}

func TestNewEngine_MalformedRuleFileIsError(t *testing.T) {
	p := writeRuleFile(t, "rules:\n  - id: a\n    expression: \"true\"\n    match: {kinds: Deployment}\n")
	if _, err := NewEngine(config.RulesConfig{RuleFiles: []string{p}}); err == nil {
		t.Fatal("expected error")
	}
}
