package envtype

import "testing"

func TestParseAliases(t *testing.T) {
	cases := map[string]Kind{
		"prd": "prod", "production": "prod", "PROD": "prod",
		"tst": "test", "testing": "test",
		"local": "localdev", "lcl": "localdev",
		"trn": "trn", "training": "trn",
		"spt": "spt", "support": "spt",
		"uat": "uat", "qa": "qa", "stg": "stg", "preprod": "preprod",
	}
	for in, want := range cases {
		s, err := Parse(in)
		if err != nil || s.ID != want {
			t.Fatalf("Parse(%q) = %+v %v, want %s", in, s, err, want)
		}
	}
	if _, err := Parse("nope"); err == nil {
		t.Fatal("unknown type must fail")
	}
}

func TestCatalogHasProdLikeAndEphemeral(t *testing.T) {
	var prod, eph bool
	for _, s := range Catalog() {
		if s.ID == Prod && s.ProdLike {
			prod = true
		}
		if s.ID == Test && s.Ephemeral {
			eph = true
		}
	}
	if !prod || !eph {
		t.Fatalf("prod/ephemeral flags missing: prod=%v eph=%v", prod, eph)
	}
}
