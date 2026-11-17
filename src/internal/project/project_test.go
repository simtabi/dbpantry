package project

import "testing"

func TestParseReadsTheLastLine(t *testing.T) {
	out := []byte("NOTICE: a hook said something\n{\"engine\":\"pg\",\"database\":\"shop\",\"secrets\":[{\"role\":\"shop_user\",\"file\":\"secrets/x\",\"action\":\"reused\"}],\"pooler\":{\"host\":\"pg-pooler\",\"port\":\"6432\"},\"block\":\"DB_HOST=pg\\n\"}\n\n")
	p, err := Parse(out)
	if err != nil || p.Database != "shop" || p.Pooler.Port != "6432" || p.Secrets[0].Action != "reused" || p.Block != "DB_HOST=pg\n" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := Parse(nil); err == nil {
		t.Error("nothing printed is an error")
	}
}
