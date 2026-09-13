package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// The work-directory contract (organization ADR-021, project ADR-0008) is a
// rule about every tool, not about one of them. Stated only in prose it gets
// re-decided by whoever adds the next tool — and prose drifts silently
// besides, because nothing compiles it. Both halves are pinned here.

var retiredWorkDirNames = []string{"workspace_root", "workspaceRoot", "workspace_dir"}

func TestNoToolSchemaOrDescriptionCarriesARetiredWorkDirName(t *testing.T) {
	h := newHarness(t)
	for _, tool := range h.srv.Tools() {
		for _, old := range retiredWorkDirNames {
			if strings.Contains(string(tool.InputSchema), `"`+old+`"`) {
				t.Errorf("tool %q declares %q; the name is work_dir", tool.Name, old)
			}
			if strings.Contains(tool.Description, old) {
				t.Errorf("tool %q describes itself with %q; the name is work_dir", tool.Name, old)
			}
		}
	}
}

// An optional work directory is an invitation to fall back to a server-owned
// default, which is the failure the contract removes. chrome-pilot shipped
// exactly that: handlers that required it, schemas that said optional.
func TestWorkDirIsRequiredWhereverItIsDeclared(t *testing.T) {
	h := newHarness(t)
	for _, tool := range h.srv.Tools() {
		var schema struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("%s: input schema is not valid JSON: %v", tool.Name, err)
		}
		if _, declared := schema.Properties["work_dir"]; !declared {
			continue
		}
		found := false
		for _, r := range schema.Required {
			if r == "work_dir" {
				found = true
			}
		}
		if !found {
			t.Errorf("tool %q declares work_dir but does not require it: an optional "+
				"work directory is an invitation to invent a default", tool.Name)
		}
	}
}
