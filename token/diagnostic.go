package token

import "sort"

type Severity uint8

const (
	SevError Severity = iota
	SevWarning
)

func (s Severity) String() string {
	if s == SevWarning {
		return "warning"
	}
	return "error"
}

// Diagnostic is a message about a span. Like every other span in the front end
// it is non-empty and resolves to raw bytes through its File.
type Diagnostic struct {
	Pos      Pos
	End      Pos
	Severity Severity
	Msg      string
}

// SortDiagnostics orders diagnostics by position, then by extent, then by
// message. The sort is stable, so diagnostics reported at the same span keep
// the order in which the phases produced them.
func SortDiagnostics(d []Diagnostic) {
	sort.SliceStable(d, func(i, j int) bool {
		switch {
		case d[i].Pos != d[j].Pos:
			return d[i].Pos < d[j].Pos
		case d[i].End != d[j].End:
			return d[i].End < d[j].End
		default:
			return d[i].Msg < d[j].Msg
		}
	})
}