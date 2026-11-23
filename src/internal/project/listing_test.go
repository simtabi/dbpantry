package project

import (
	"reflect"
	"testing"
)

func TestJoinCallsEachProjectByWhatItsEngineSays(t *testing.T) {
	l, err := ParseListing([]byte("[project] a log line\n" +
		`{"engines":[{"engine":"pg","title":"PostgreSQL","reachable":true,"databases":[` +
		`{"name":"postgres","extensions":[]},{"name":"shop","extensions":["vector"]},{"name":"legacy","extensions":[]}]},` +
		`{"engine":"mysql","title":"MySQL","reachable":false,"databases":[]}]}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := l.Join([]Known{{"pg", "shop"}, {"pg", "gone"}, {"mysql", "blog"}, {"mongodb", "logs"}})
	want := []Entry{
		{Engine: "pg", Name: "shop", State: Active, Extensions: []string{"vector"}},
		{Engine: "pg", Name: "gone", State: Orphaned},
		{Engine: "mysql", Name: "blog", State: Unknown},
		{Engine: "mongodb", Name: "logs", State: Unknown},
		{Engine: "pg", Name: "legacy", State: Unmanaged, Extensions: []string{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseListingNeedsOutput(t *testing.T) {
	if _, err := ParseListing([]byte("\n")); err == nil {
		t.Error("nothing printed is an error")
	}
}
