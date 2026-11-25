package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The dashboard's image listing runs on the real runtime CLI. Its format named
// {{.Label "..."}}, which `docker images` has never had (`docker ps` has it):
// Docker 29.8 answers "can't evaluate field Label in type
// *formatter.imageContext", and the expression's `|| true` turned that into an
// empty list, so the Images page said "No images built yet" beside eight built
// images (2026-11-25). The stub below answers as Docker 29 does.
const dockerStub = `#!/usr/bin/env bash
case "$1" in
  info) exit 0 ;;
  --version) echo "Docker version 29.8.2"; exit 0 ;;
  compose) echo "2.40.0"; exit 0 ;;
  images)
    [ -n "${STUB_DOWN:-}" ] && { echo "Cannot connect to the Docker daemon" >&2; exit 1; }
    case "$*" in *".Label"*) echo "template parsing error: can't evaluate field Label in type *formatter.imageContext" >&2; exit 1 ;; esac
    printf 'localhost/dbpantry-pg:17\t1.2GB\t2 hours ago\n'
    printf 'docker.io/library/debian:12-slim\t74MB\t3 days ago\n'
    printf 'localhost/dbpantry-caddy:dev\t165MB\t1 hour ago\n'
    exit 0 ;;
  image)
    shift
    [ "$1" = inspect ] || exit 0
    shift
    while [ $# -gt 0 ]; do
      case "$1" in
        --format|-f) shift ;;
        localhost/dbpantry-pg:17) echo unsupported ;;
        *) echo "" ;;
      esac
      shift
    done
    exit 0 ;;
esac
exit 0
`

func imagesWithStub(t *testing.T, down bool) ([][]string, error) {
	t.Helper()
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(dockerStub), 0o700); err != nil { // #nosec G306 -- an executable stub in a test directory
		t.Fatal(err)
	}
	home := t.TempDir()
	env := []string{
		"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, "MDB_HOME=" + home,
		"CONTAINER_RUNTIME=docker", "MDB_ENV_FILE=" + filepath.Join(repo, ".env.example"),
	}
	if down {
		env = append(env, "STUB_DOWN=1")
	}
	a := &app{}
	return a.records(context.Background(), repo, env, imagesScript)
}

func TestTheImagesPageListsOurImagesOnDocker29(t *testing.T) {
	rows, err := imagesWithStub(t, false)
	if err != nil {
		t.Fatalf("the image listing failed: %v", err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, strings.Join(r, "|"))
	}
	want := []string{"localhost/dbpantry-pg:17|1.2GB|2 hours ago|unsupported", "localhost/dbpantry-caddy:dev|165MB|1 hour ago|"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("images:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A runtime that does not answer is an outage, never "no images built".
func TestAnImageListingOutageIsAnError(t *testing.T) {
	if rows, err := imagesWithStub(t, true); err == nil {
		t.Fatalf("a daemon that does not answer listed %d images and no error", len(rows))
	}
}
