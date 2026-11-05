package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is the checkout this module lives in (src/internal/engine/../../..).
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestParseAcceptsTheSubset(t *testing.T) {
	src := `# comment
  # indented comment

MDB_ENGINE_NAME=pg
MDB_ENGINE_TITLE="PostgreSQL"
MDB_ENGINE_PING='pg_isready -U "postgres"'
MDB_ENGINE_EMPTY=
MDB_ENGINE_QUOTED_EMPTY=''
MDB_ENGINE_IMAGE='docker.io/x/y:1@sha256:ab'
MDB_ENGINE_LIST="17 18"
`
	d, err := Parse(strings.NewReader(src), "t.conf")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"MDB_ENGINE_NAME":         "pg",
		"MDB_ENGINE_TITLE":        "PostgreSQL",
		"MDB_ENGINE_PING":         `pg_isready -U "postgres"`,
		"MDB_ENGINE_EMPTY":        "",
		"MDB_ENGINE_QUOTED_EMPTY": "",
		"MDB_ENGINE_IMAGE":        "docker.io/x/y:1@sha256:ab",
		"MDB_ENGINE_LIST":         "17 18",
	}
	got := d.Map()
	if len(got) != len(want) {
		t.Fatalf("got %d keys, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := d.Get("MDB_ENGINE_EMPTY"); !ok {
		t.Error("an empty assignment must still be present")
	}
}

func TestParseRefusesWhatTheShellWouldInterpret(t *testing.T) {
	cases := map[string]string{
		"command substitution": "MDB_ENGINE_X=$(id)\n",
		"expansion in dquotes": "MDB_ENGINE_X=\"$HOME\"\n",
		"backtick":             "MDB_ENGINE_X=\"`id`\"\n",
		"backslash":            "MDB_ENGINE_X=\"a\\b\"\n",
		"bare space":           "MDB_ENGINE_X=a b\n",
		"bare semicolon":       "MDB_ENGINE_X=a;b\n",
		"trailing after quote": "MDB_ENGINE_X='a' # note\n",
		"unterminated":         "MDB_ENGINE_X='a\n",
		"wrong prefix":         "MDB_PG_VERSION=17\n",
		"export":               "export MDB_ENGINE_X=a\n",
		"indented assignment":  "  MDB_ENGINE_X=a\n",
		"statement":            "source other.sh\n",
		"duplicate":            "MDB_ENGINE_X=a\nMDB_ENGINE_X=b\n",
		"lower-case key":       "MDB_ENGINE_x=a\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(src), "t.conf"); err == nil {
				t.Fatalf("accepted %q", src)
			}
		})
	}
}

func TestParseErrorNamesTheLine(t *testing.T) {
	_, err := Parse(strings.NewReader("# a\nMDB_ENGINE_X=ok\nMDB_ENGINE_Y=$(id)\n"), "e.conf")
	if err == nil || !strings.Contains(err.Error(), "e.conf:3:") {
		t.Fatalf("error %v does not name e.conf:3", err)
	}
}

func TestRepositoryDescriptorsLoad(t *testing.T) {
	engines, err := Load(filepath.Join(repoRoot(t), "engines"))
	if err != nil {
		t.Fatal(err)
	}
	if len(engines) < 6 {
		t.Fatalf("loaded %d engines, want at least the 6 shipped ones", len(engines))
	}
	for i := 1; i < len(engines); i++ {
		if engines[i-1].Name >= engines[i].Name {
			t.Fatalf("engines not sorted by name: %s before %s", engines[i-1].Name, engines[i].Name)
		}
	}
	m := NewManifest(engines)
	pg, ok := m.Engine("pg")
	if !ok {
		t.Fatal("no pg engine")
	}
	if pg.Port != 5432 || pg.RootSecretFile != "/run/secrets/pg_superuser_password.txt" || !pg.Implemented {
		t.Errorf("pg typed wrong: %+v", pg)
	}
	if pg.Pooler == nil || pg.Pooler.Service != "pg-pooler" || pg.Pooler.Superuser {
		t.Errorf("pg pooler: %+v", pg.Pooler)
	}
	if fe, _ := m.Engine("ferretdb"); fe.Implemented {
		t.Error("ferretdb has no image yet and must not be implemented")
	}
}

// The required-key list exists twice, here and in scripts/engine-lib.sh. This
// keeps them one list.
func TestRequiredKeysMatchEngineLib(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "engine-lib.sh"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)MDB_ENGINE_REQUIRED_KEYS="\n(.*?)"`).FindSubmatch(b)
	if m == nil {
		t.Fatal("MDB_ENGINE_REQUIRED_KEYS not found in engine-lib.sh")
	}
	got := strings.Fields(string(m[1]))
	if strings.Join(got, " ") != strings.Join(RequiredKeys, " ") {
		t.Fatalf("engine-lib.sh requires\n  %v\nGo requires\n  %v", got, RequiredKeys)
	}
}

func TestParadigmVocabulary(t *testing.T) {
	want := "relational document wide-column key-value graph search vector time-series"
	var got []string
	for _, p := range Paradigms() {
		got = append(got, string(p))
	}
	if strings.Join(got, " ") != want {
		t.Fatalf("vocabulary %v, want %s", got, want)
	}
	if Paradigm("columnar").Valid() {
		t.Error("columnar is not in the vocabulary")
	}
}

// writeEngine writes a minimal valid descriptor tree and returns its engines dir.
func writeEngine(t *testing.T, extra string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "engines", "x")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "images"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "images", "bases.tsv"), []byte("x\t1\tdocker.io/library/x:1@sha256:00\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conf := `MDB_ENGINE_NAME=x
MDB_ENGINE_TITLE=X
MDB_ENGINE_FAMILY=x
MDB_ENGINE_PARADIGM=relational
MDB_ENGINE_VERSIONS=1
MDB_ENGINE_DEFAULT_VERSION=1
MDB_ENGINE_PORT=1
MDB_ENGINE_PORT_OFFSET=9
MDB_ENGINE_DATA_DIR=/d
MDB_ENGINE_USER=u
MDB_ENGINE_ROOT_SECRET=x_root_password.txt
MDB_ENGINE_PING=p
MDB_ENGINE_CLIENT=c
MDB_ENGINE_DUMP=d
MDB_ENGINE_RESTORE=r
MDB_ENGINE_BACKUP_EXT=e
MDB_ENGINE_AUTH_METHOD=a
MDB_ENGINE_POOLING=driver
MDB_ENGINE_LICENSE=MIT
MDB_ENGINE_OSI_APPROVED=true
MDB_ENGINE_PUBLISH=reference
` + extra
	if err := os.WriteFile(filepath.Join(dir, "engine.conf"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "engines")
}

func TestParadigmAlso(t *testing.T) {
	es, err := Load(writeEngine(t, "MDB_ENGINE_PARADIGM_ALSO=time-series\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := es[0].Paradigms; len(got) != 2 || got[1] != TimeSeries {
		t.Fatalf("paradigms %v", got)
	}
	for _, bad := range []string{"MDB_ENGINE_PARADIGM_ALSO=columnar\n", "MDB_ENGINE_PARADIGM_ALSO=relational\n"} {
		if _, err := Load(writeEngine(t, bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := Load(writeEngine(t, "MDB_ENGINE_PARADIGM_X=1\n")); err != nil {
		t.Errorf("an unknown optional key must load (descriptors are add-only): %v", err)
	}
}

func TestBootBudget(t *testing.T) {
	es, err := Load(writeEngine(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if es[0].BootBudget != BootBudgetSeconds {
		t.Errorf("budget %d", es[0].BootBudget)
	}
	es, err = Load(writeEngine(t, "MDB_ENGINE_HEAVY=true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if es[0].BootBudget != HeavyBootBudgetSeconds {
		t.Errorf("heavy budget %d", es[0].BootBudget)
	}
}

func TestPrincipalsArePathsOnly(t *testing.T) {
	es, err := Load(writeEngine(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	e := es[0]
	all := append(append([]Principal(nil), e.Principals...), e.ProjectPrincipals("shop")...)
	roles := []string{"admin", "app", "read-only"}
	if len(all) != len(roles) {
		t.Fatalf("principals %v", all)
	}
	for i, p := range all {
		if p.Role != roles[i] || !strings.HasPrefix(p.PasswordFile, SecretsDir+"/") || !strings.HasSuffix(p.PasswordFile, ".txt") {
			t.Errorf("principal %d: %+v", i, p)
		}
	}
	if all[1].PasswordFile != "/run/secrets/x_shop_password.txt" || all[2].User != "shop_readonly" {
		t.Errorf("project principals do not match scripts/new-project: %+v", all[1:])
	}
}

func TestRequiredAndNumericValidation(t *testing.T) {
	for _, bad := range []string{
		"MDB_ENGINE_PARADIGM=columnar\n", // duplicates the key: also refused
	} {
		if _, err := Load(writeEngine(t, bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	dir := writeEngine(t, "")
	conf := filepath.Join(dir, "x", "engine.conf")
	b, _ := os.ReadFile(conf) // #nosec G304 -- a file this test wrote
	for _, edit := range [][2]string{
		{"MDB_ENGINE_PORT=1", "MDB_ENGINE_PORT=one"},
		{"MDB_ENGINE_PARADIGM=relational", "MDB_ENGINE_PARADIGM=columnar"},
		{"MDB_ENGINE_TITLE=X", "MDB_ENGINE_TITLE="},
		{"MDB_ENGINE_DEFAULT_VERSION=1", "MDB_ENGINE_DEFAULT_VERSION=2"},
		{"MDB_ENGINE_OSI_APPROVED=true", "MDB_ENGINE_OSI_APPROVED=yes"},
	} {
		if err := os.WriteFile(conf, bytes.Replace(b, []byte(edit[0]), []byte(edit[1]), 1), 0o600); err != nil { // #nosec G703 -- a file this test wrote
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Errorf("accepted %s", edit[1])
		}
	}
}

func TestMatrixAndTSV(t *testing.T) {
	es, err := Load(writeEngine(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManifest(es)
	var buf bytes.Buffer
	if err := m.WriteMatrix(&buf, "versions"); err != nil {
		t.Fatal(err)
	}
	// A referenced engine is not built: an empty matrix is [], never null.
	if buf.String() != "[]\n" {
		t.Errorf("matrix %q", buf.String())
	}
	if err := m.WriteMatrix(&buf, "bogus"); err == nil {
		t.Error("an unknown mode must fail")
	}
	buf.Reset()
	if err := m.WriteTSV(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "x\tMDB_ENGINE_PORT\t1\n") {
		t.Errorf("tsv:\n%s", buf.String())
	}
	buf.Reset()
	if err := m.WriteEngines(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "x\ttrue\tfalse\t-\n" {
		t.Errorf("engines: %q", buf.String())
	}
}

func TestCheckHelmMatchesTheChartAndCatchesDrift(t *testing.T) {
	root := repoRoot(t)
	es, err := Load(filepath.Join(root, "engines"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManifest(es)
	values, err := os.ReadFile(filepath.Join(root, "deploy", "helm", "dbpantry", "values.yaml")) // #nosec G304 -- the repository's own chart
	if err != nil {
		t.Fatal(err)
	}
	problems, err := m.CheckHelm(values)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("the chart disagrees with engines/: %v", problems)
	}
	// Mutation: a port the chart restates differently must be reported.
	drifted := bytes.Replace(values, []byte("    port: 9042"), []byte("    port: 9043"), 1)
	if bytes.Equal(drifted, values) {
		t.Fatal("mutation did not apply; the test no longer edits the chart")
	}
	problems, _ = m.CheckHelm(drifted)
	if len(problems) != 1 || !strings.Contains(problems[0], "engines.cassandra.port") {
		t.Fatalf("drift not reported: %v", problems)
	}
}
