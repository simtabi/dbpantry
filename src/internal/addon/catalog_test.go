package addon

import (
	"testing"

	"github.com/simtabi/dbpantry/src/internal/engine"
)

func TestCatalogHasPgBouncerAndNoMongoPooler(t *testing.T) {
	engines := []engine.Engine{
		{Name: "pg", Title: "PostgreSQL", Driver: "pgsql", Pooling: "external", Pooler: &engine.Pooler{Service: "pg-pooler", Image: "localhost/dbpantry-pgbouncer:dev"}, Implemented: true},
		{Name: "mongodb", Title: "MongoDB", Driver: "mongodb", Pooling: "driver", PoolingRationale: "drivers implement CMAP", Implemented: true},
	}
	cat := Catalog(engines)
	pg, ok := ByID(cat, "pooler.pg")
	if !ok || !pg.Supported || pg.Profile != "pg-pooler" {
		t.Fatalf("pooler.pg = %+v ok=%v", pg, ok)
	}
	mongo, ok := ByID(cat, "pooler.mongodb")
	if !ok || mongo.Supported {
		t.Fatalf("mongodb pooler must be unsupported: %+v", mongo)
	}
	ext, ok := ByID(cat, "ext.vector")
	if !ok || !ext.Supported || ext.Kind != KindExtension {
		t.Fatalf("ext.vector = %+v", ext)
	}
}

func TestForStatusPageDropsUnsupportedAndDisabledEngines(t *testing.T) {
	rows := []Status{
		{Spec: Spec{ID: "pooler.pg", Supported: true}, Engine: "pg", EngineEnabled: true, Enabled: true},
		{Spec: Spec{ID: "pooler.mongodb", Supported: false}, Engine: "mongodb", EngineEnabled: true},
		{Spec: Spec{ID: "ext.vector", Supported: true}, Engine: "pg", EngineEnabled: false},
	}
	got := ForStatusPage(rows)
	if len(got) != 1 || got[0].ID != "pooler.pg" {
		t.Fatalf("got %+v", got)
	}
}

func TestGroupByEngineBucketsKind(t *testing.T) {
	rows := []Status{
		{Spec: Spec{ID: "ext.vector", Title: "vector", Kind: KindExtension, Supported: true}, Engine: "pg", Enabled: true},
		{Spec: Spec{ID: "pooler.pg", Title: "pgBouncer", Kind: KindPooler, Supported: true}, Engine: "pg", Enabled: true},
		{Spec: Spec{ID: "ext.citext", Title: "citext", Kind: KindExtension, Supported: true}, Engine: "pg"},
		{Spec: Spec{ID: "ui.adminer", Title: "Adminer", Kind: KindUI, Supported: true}, Engine: "mysql"},
	}
	got := GroupByEngine(rows, func(e string) string {
		if e == "pg" {
			return "PostgreSQL"
		}
		return "MySQL"
	})
	if len(got) != 2 || got[0].Engine != "mysql" || got[1].Engine != "pg" {
		t.Fatalf("engine order = %+v", got)
	}
	pg := got[1]
	if pg.Title != "PostgreSQL" || pg.On != 2 || pg.Total != 3 {
		t.Fatalf("pg group = %+v", pg)
	}
	if len(pg.Kinds) != 2 || pg.Kinds[0].Kind != KindPooler || pg.Kinds[1].Kind != KindExtension {
		t.Fatalf("kinds = %+v", pg.Kinds)
	}
	if pg.Kinds[1].Rows[0].Title != "citext" {
		t.Fatalf("extensions should sort by title: %+v", pg.Kinds[1].Rows)
	}
}

func TestMergeProfiles(t *testing.T) {
	if got := MergeProfiles("pg,ui", "pg-pooler", true); got != "pg,ui,pg-pooler" {
		t.Fatalf("enable = %q", got)
	}
	if got := MergeProfiles("pg,pg-pooler,ui", "pg-pooler", false); got != "pg,ui" {
		t.Fatalf("disable = %q", got)
	}
}
