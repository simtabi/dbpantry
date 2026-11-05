package engine

// Paradigm is a data model an engine serves.
type Paradigm string

// The vocabulary, shared with mesobia (its spec 04, adopted as N11 of the
// refactor plan). Check 31 enforces the same eight values on the descriptors.
const (
	Relational Paradigm = "relational"
	Document   Paradigm = "document"
	WideColumn Paradigm = "wide-column"
	KeyValue   Paradigm = "key-value"
	Graph      Paradigm = "graph"
	Search     Paradigm = "search"
	Vector     Paradigm = "vector"
	TimeSeries Paradigm = "time-series"
)

// Paradigms lists the vocabulary in its documented order.
func Paradigms() []Paradigm {
	return []Paradigm{Relational, Document, WideColumn, KeyValue, Graph, Search, Vector, TimeSeries}
}

// Valid reports whether p is in the vocabulary.
func (p Paradigm) Valid() bool {
	for _, v := range Paradigms() {
		if v == p {
			return true
		}
	}
	return false
}
