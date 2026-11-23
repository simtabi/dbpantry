package envtype

import (
	"path/filepath"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "envs.json")
	e := Entry{Name: "shop-uat", Type: UAT, Home: filepath.Join(t.TempDir(), "uat"), Project: "mdb-shop-uat"}
	if err := Upsert(path, e); err != nil {
		t.Fatal(err)
	}
	got, err := Get(path, "shop-uat")
	if err != nil || got.Type != UAT || got.Project != "mdb-shop-uat" {
		t.Fatalf("%+v %v", got, err)
	}
	home, err := ResolveHome(path, "shop-uat")
	if err != nil || home != got.Home {
		t.Fatalf("resolve = %q %v", home, err)
	}
	if err := Delete(path, "shop-uat"); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(path, "shop-uat"); err == nil {
		t.Fatal("deleted entry still present")
	}
}

func TestValidName(t *testing.T) {
	if !ValidName("shop-uat") || ValidName("Shop") || ValidName("") || ValidName("1bad") {
		t.Fatal("ValidName bounds wrong")
	}
}
