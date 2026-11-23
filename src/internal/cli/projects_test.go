package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/simtabi/dbpantry/src/internal/project"
)

func TestProjectLsJoinsTheEnginesAndThePasswordFiles(t *testing.T) {
	root, log := stubRoot(t)
	listing := `#!/usr/bin/env bash
echo '[project] noise on stdout before the object'
echo '{"engines":[{"engine":"pg","title":"PostgreSQL","reachable":true,"databases":[{"name":"postgres","extensions":[]},{"name":"shop","extensions":["vector"]},{"name":"legacy","extensions":[]}]}]}'
`
	if err := os.WriteFile(filepath.Join(root, "scripts", "project"), []byte(listing), 0o700); err != nil { // #nosec G306 -- an executable stub in a test directory
		t.Fatal(err)
	}
	secrets := filepath.Join(root, "secrets")
	for _, f := range []string{"pg_shop_user_password.txt", "pg_gone_user_password.txt", "mysql_blog_user_password.txt", "pg_superuser_password.txt"} {
		if err := os.MkdirAll(secrets, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(secrets, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	code, out, errOut, _ := runStub(t, root, log, nil, "", "project", "ls", "-o", "json")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got []project.Entry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := []project.Entry{
		{Engine: "mysql", Name: "blog", State: project.Unknown},
		{Engine: "pg", Name: "gone", State: project.Orphaned},
		{Engine: "pg", Name: "shop", State: project.Active, Extensions: []string{"vector"}},
		{Engine: "pg", Name: "legacy", State: project.Unmanaged, Extensions: []string{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}

	if code, _, _, _ := runStub(t, root, log, nil, "", "project", "ls", "-o", "yaml"); code != ExitUsage {
		t.Errorf("-o yaml: exit %d, want the usage error", code)
	}
}
