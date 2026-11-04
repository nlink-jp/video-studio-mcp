package tools

// The bundled presentation-video skill is installed into an agent's skill
// directory and read as instruction, so a name in it that the server does not
// have is the agent being told to make a call the server refuses. These tests
// hold the skill to the server in both directions (organization ADR-027,
// project ADR-0010):
//
//   - every tool the server registers is named by the skill, and every
//     identifier the skill puts in backticks exists in the server — taken from
//     the registered tools, their input schemas, the toolerr codes and the
//     json tags of the server's own types, not from a hand-copied list;
//   - every retired name is held absent;
//   - the manifest example parses with the server's own parser.

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nlink-jp/video-studio-mcp/internal/manifest"
)

// skillDir is the skill at the repository root, where the organization's
// validator (tests/validate-skill.sh) and `make package-skill` find it.
const skillDir = "../../presentation-video"

// readSkillCorpus concatenates every Markdown file of the skill.
func readSkillCorpus(t *testing.T) string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(skillDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("reading the skill: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("the skill has no Markdown; is %s present?", skillDir)
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(files[n])
		b.WriteByte('\n')
	}
	return b.String()
}

func TestSkillFrontmatter(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("the skill has no SKILL.md: %v", err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatalf("SKILL.md must start with YAML frontmatter")
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		t.Fatalf("SKILL.md frontmatter is not terminated")
	}
	front := s[:end+5]
	for _, field := range []string{"name: presentation-video\n", "description: ", "argument-hint: "} {
		if !strings.Contains(front, field) {
			t.Errorf("frontmatter missing %q", field)
		}
	}
}

func TestSkillNamesEveryRegisteredTool(t *testing.T) {
	s := readSkillCorpus(t)
	for _, tool := range newHarness(t).srv.Tools() {
		if !strings.Contains(s, "`"+tool.Name+"`") {
			t.Errorf("the skill does not name the tool %q", tool.Name)
		}
	}
}

// otherServerNames are backticked identifiers that belong to the servers
// upstream of this one in the chain the skill drives. They are not this
// server's, so this repository cannot derive them; each is listed with its
// owner so a rename there is a deliberate edit here.
var otherServerNames = map[string]string{
	"list_speakers":       "voice-studio tool",
	"register_dictionary": "voice-studio tool",
	"synthesize_line":     "voice-studio tool",
	"pause_after_ms":      "voice-studio script field",
	"credits_path":        "voice-studio master result",
	"unverified_models":   "voice-studio master result",
}

// snakeIdent is an identifier in backticks shaped like a tool, code, argument
// or field name: lower-case words joined by underscores.
var snakeIdent = regexp.MustCompile("`([a-z]+(?:_[a-z0-9]+)+)`")

// A retired or misspelled name in the skill passes every "does the skill
// mention X" check; only a closed vocabulary catches it.
func TestSkillNamesOnlyWhatTheServerHas(t *testing.T) {
	known := serverVocabulary(t)
	seen := map[string]bool{}
	for _, m := range snakeIdent.FindAllStringSubmatch(readSkillCorpus(t), -1) {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		if known[name] {
			continue
		}
		if _, ok := otherServerNames[name]; ok {
			continue
		}
		t.Errorf("the skill names `%s`, which is not a tool, argument, error code or field of this server; "+
			"fix the skill, or list it in otherServerNames with its owner", name)
	}
	for name := range otherServerNames {
		if known[name] {
			t.Errorf("otherServerNames lists %q, but this server has it; remove it from the list", name)
		}
		if !seen[name] {
			t.Errorf("otherServerNames lists %q, which the skill no longer uses; remove it from the list", name)
		}
	}
}

// The codes the skill's hand-off table explains. Checked against the server,
// so the list cannot name a code that does not exist.
func TestSkillExplainsTheHandOffErrors(t *testing.T) {
	codes := toolerrCodes(t)
	s := readSkillCorpus(t)
	for _, code := range []string{
		"manifest_incomplete", "invalid_manifest", "probe_failed",
		"job_not_found", "work_dir_required",
	} {
		if !codes[code] {
			t.Errorf("this test expects the code %q, which the server no longer has", code)
			continue
		}
		if !strings.Contains(s, "`"+code+"`") {
			t.Errorf("the skill does not explain %q", code)
		}
	}
}

func TestSkillNamesNoRetiredName(t *testing.T) {
	s := readSkillCorpus(t)
	for _, old := range append([]string{"~/.video-studio"}, retiredWorkDirNames...) {
		if strings.Contains(s, old) {
			t.Errorf("the skill still teaches %q (ADR-0008 replaced it with a required work_dir)", old)
		}
	}
}

// The manifest example is what an agent copies; it must pass the parser that
// rejects unknown fields and transitions.
func TestSkillManifestExampleParses(t *testing.T) {
	block := regexp.MustCompile("(?s)```jsonl\n(.*?)```")
	var found int
	for _, m := range block.FindAllStringSubmatch(readSkillCorpus(t), -1) {
		if !strings.Contains(m[1], `"image"`) {
			continue
		}
		found++
		pages, err := manifest.Parse([]byte(m[1]))
		if err != nil {
			t.Errorf("the skill's manifest example does not parse: %v", err)
			continue
		}
		if len(pages) == 0 {
			t.Error("the skill's manifest example has no pages")
		}
	}
	if found == 0 {
		t.Fatal("the skill has no manifest example (a ```jsonl block with \"image\")")
	}
}

// serverVocabulary is every name the server exposes: its tools, every
// property of their input schemas, its error codes, and the json tags of its
// own types (the manifest and the results it returns).
func serverVocabulary(t *testing.T) map[string]bool {
	t.Helper()
	known := map[string]bool{}
	for _, tool := range newHarness(t).srv.Tools() {
		known[tool.Name] = true
		var schema any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("%s: input schema: %v", tool.Name, err)
		}
		collectSchemaProperties(schema, known)
	}
	for code := range toolerrCodes(t) {
		known[code] = true
	}
	tag := regexp.MustCompile(`json:"([a-z0-9_]+)`)
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range tag.FindAllSubmatch(src, -1) {
			known[string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return known
}

func collectSchemaProperties(v any, into map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if props, ok := x["properties"].(map[string]any); ok {
			for name := range props {
				into[name] = true
			}
		}
		for _, child := range x {
			collectSchemaProperties(child, into)
		}
	case []any:
		for _, child := range x {
			collectSchemaProperties(child, into)
		}
	}
}

// toolerrCodes reads the Code* constants of internal/toolerr: the codes the
// server can return, as declared, not as copied.
func toolerrCodes(t *testing.T) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "../toolerr/toolerr.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if !strings.HasPrefix(name.Name, "Code") || i >= len(vs.Values) {
				continue
			}
			if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil {
					codes[v] = true
				}
			}
		}
		return true
	})
	if len(codes) < 10 {
		t.Fatalf("found only %d toolerr codes; has internal/toolerr changed shape?", len(codes))
	}
	return codes
}
