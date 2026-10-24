package apply

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

// large is a target of 500 members with ten lines each and 50 groups with
// ten seats each, the size of a household's evidence: most of it is what a
// diff doesn't touch.
func large(b *testing.B) proto.Message {
	var s strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&s, "members { id: \"m%03d\" name: \"Member %d\"", i, i)
		for j := 0; j < 10; j++ {
			fmt.Fprintf(&s, " lines { text: \"line %d\" qty: %d }", j, j)
		}
		s.WriteString(" }\n")
	}
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&s, "groups { number: %d title: \"Group %d\"", i+1, i)
		for j := 0; j < 10; j++ {
			fmt.Fprintf(&s, " seats { code: \"s%d\" }", j)
		}
		s.WriteString(" }\n")
	}
	return rootOf(b, s.String())
}

// renameOne patches one member's name, the size of a keystroke.
const renameOne = `members { id: "m250" patch { name: "Renamed" } }`

// BenchmarkApply applies one small diff to the large target with Apply,
// which checks the whole target each time.
func BenchmarkApply(b *testing.B) {
	a, d := large(b), diffOf(b, renameOne)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Apply(a, d); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkValidate validates the small diff against the large target.
func BenchmarkValidate(b *testing.B) {
	a, d := large(b), diffOf(b, renameOne)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Validate(a, d); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTargetApply applies the small diff to the large target through
// a Target, which checked the target once, before the timer.
func BenchmarkTargetApply(b *testing.B) {
	_, p := root(b)
	a, d := large(b), diffOf(b, renameOne)
	target, err := NewTarget(a, p.body)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := target.Apply(d); err != nil {
			b.Fatal(err)
		}
	}
}
