package apply

import (
	"math/rand/v2"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// parse returns a message of the test schema from text.
func parse(t testing.TB, name protoreflect.FullName, s string) proto.Message {
	t.Helper()
	root(t)
	m := dynamicpb.NewMessage(loaded.Message(name))
	if err := prototext.Unmarshal([]byte(s), m); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

func rootOf(t testing.TB, s string) proto.Message { return parse(t, "pdgtest.v1.Root", s) }
func diffOf(t testing.TB, s string) proto.Message { return parse(t, "pdgtest.v1.diff.RootDiff", s) }

func TestValidateRefusals(t *testing.T) {
	const base = `
		name: "r"
		count: 1
		members { id: "a" name: "Ada" lines { text: "x" } lines { text: "y" } }
		members { id: "b" }
		counts { key: "k" value: 1 }
		configs { key: 1 value { theme: "t" } }
		legacy { entries { key: "e" } }
		scores: 1 scores: 2
		cash: "c"
		coins { heads: true value: 1 }
	`
	tests := []struct {
		name, a, d, want string
		unknown          string // "a" or "d": add an unknown field there
	}{
		{"adds a key the list has", base, `members { id: "a" add { id: "a" } }`, "adds a key the list has", ""},
		{"adds a key the map has", base, `configs { key: 1 add {} }`, "adds a key the map has", ""},
		{"removes a key the list lacks", base, `members { id: "z" remove {} }`, "removes a key the list lacks", ""},
		{"removes a key the map lacks", base, `counts { key: "z" remove {} }`, "removes a key the map lacks", ""},
		{"patches a key the list lacks", base, `members { id: "z" patch { name: "Z" } }`, "patches a key the list lacks", ""},
		{"patches a key the map lacks", base, `configs { key: 9 patch { theme: "u" } }`, "patches a key the map lacks", ""},
		{"two entries for one key", base, `members { id: "a" remove {} } members { id: "a" patch {} }`, "two changes for one key", ""},
		{"two entries for one map key", base, `counts { key: "k" set: 2 } counts { key: "k" remove {} }`, "two changes for one key", ""},
		{"a change wrapper with no op", base, `members { id: "a" }`, "a change with no op", ""},
		{"an indexed edit with no op", base, `scores { index: 0 }`, "an edit with no op", ""},
		{"a wrapper key that differs from its add's key", base, `members { id: "c" add { id: "d" } }`, "the add's key is d", ""},
		{"a patch that changes a keyed item's key", base, `members { id: "a" patch { id: "z" } }`, "the patch changes the key", ""},
		{"a patch that changes an atomic item's key", base, `coins { heads: true patch { heads: false } }`, "the patch changes the key", ""},
		{"a patch that clears an optional key", base, `legacy { entries { key: "e" patch { unset: 1 } } }`, "the patch changes the key", ""},
		{"clears a field without explicit presence", base, `unset: 1`, "clears name, which has no explicit presence", ""},
		{"clears a repeated field", base, `unset: 17`, "clears scores, which has no explicit presence", ""},
		{"clears a field the target doesn't have", base, `unset: 99`, "clears field 99", ""},
		{"both sets and clears one field", base, `count: 2 unset: 2`, "both sets and clears count", ""},
		{"an order missing a key", base, `members_order: "a"`, "the order doesn't name exactly the resulting keys", ""},
		{"an order naming a removed key", base, `members { id: "b" remove {} } members_order: "a" members_order: "b"`, "the order doesn't name exactly", ""},
		{"an order naming a key twice", base, `members_order: "a" members_order: "b" members_order: "a"`, "the order doesn't name exactly", ""},
		{"a patch index outside [0, n)", base, `scores { index: 2 patch: 5 }`, "patch index 2 is outside [0, 2)", ""},
		{"a remove index outside [0, n)", base, `members { id: "a" patch { lines { index: 2 remove {} } } }`, "remove index 2 is outside [0, 2)", ""},
		{"an insert index outside [0, n]", base, `scores { index: 3 insert_before { items: 1 } }`, "insert index 3 is outside [0, 2]", ""},
		{"a second insert at one index", base, `scores { index: 2 insert_before { items: 1 } } scores { index: 2 insert_before { items: 2 } }`, "a second insert at one index", ""},
		{"a patch and a remove at one index", base, `scores { index: 0 patch: 3 } scores { index: 0 remove {} }`, "two of patch and remove at one index", ""},
		{"two patches at one index", base, `scores { index: 1 patch: 3 } scores { index: 1 patch: 4 }`, "two of patch and remove at one index", ""},
		{"an insert with no elements", base, `scores { index: 0 insert_before {} }`, "an insert with no elements", ""},
		{"an optional key unset in an add", base, `legacy { entries { key: "f" add { value: 1 } } }`, "the add leaves the key key unset", ""},
		{"unknown fields in d", base, `members { id: "a" patch { name: "n" } }`, "d.members[0].patch has unknown fields", "d"},
		{"unknown fields in a", base + ` settings { theme: "t" }`, ``, "a.settings has unknown fields", "a"},
		{"a keyed list with two items for one key", `members { id: "a" } members { id: "a" }`, ``, "two items have the key a", ""},
		{"a keyed list with an unset optional key", `legacy { entries { value: 1 } }`, ``, "the key key is unset", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, d := rootOf(t, tt.a), diffOf(t, tt.d)
			switch tt.unknown {
			case "a":
				addUnknown(a.ProtoReflect().Get(a.ProtoReflect().Descriptor().Fields().ByName("settings")).Message())
			case "d":
				dm := d.ProtoReflect()
				w := dm.Get(dm.Descriptor().Fields().ByName("members")).List().Get(0).Message()
				addUnknown(w.Get(w.Descriptor().Fields().ByName("patch")).Message())
			}
			err := Validate(a, d)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate returned %v, want an error containing %q", err, tt.want)
			}
			if _, err := Apply(a, d); err == nil {
				t.Fatal("Apply accepted the diff")
			}
		})
	}
}

func addUnknown(m protoreflect.Message) {
	m.SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 9999, protowire.VarintType), 1))
}

func TestValidateAccepts(t *testing.T) {
	const base = `members { id: "a" } scores: 1 scores: 2 cash: "c" counts { key: "k" value: 1 }`
	for _, d := range []string{
		`members { id: "a" patch { id: "a" } }`,
		`scores { index: 0 insert_before { items: 9 } } scores { index: 0 remove {} } scores { index: 2 insert_before { items: 8 } }`,
		`credit { units: 1 } unset: 23`,
		`counts { key: "k" set: 2 } counts { key: "n" set: 3 }`,
		`unset: 2 unset: 2`,
	} {
		if err := Validate(rootOf(t, base), diffOf(t, d)); err != nil {
			t.Errorf("Validate(%s): %v", d, err)
		}
	}
}

func TestApply(t *testing.T) {
	tests := []struct{ name, a, d, want string }{
		{
			"an insert comes before its index's element, whatever is done to it",
			`scores: 1 scores: 2 scores: 3`,
			`scores { index: 1 remove {} } scores { index: 1 insert_before { items: 7 items: 8 } } scores { index: 0 patch: 5 } scores { index: 3 insert_before { items: 9 } }`,
			`scores: 5 scores: 7 scores: 8 scores: 3 scores: 9`,
		},
		{
			"adds go after the kept items in key order, unless the order says otherwise",
			`members { id: "b" } members { id: "a" }`,
			`members { id: "d" add { id: "d" } } members { id: "c" add { id: "c" } }`,
			`members { id: "b" } members { id: "a" } members { id: "c" } members { id: "d" }`,
		},
		{
			"an order field sorts the list",
			`members { id: "b" } members { id: "a" }`,
			`members { id: "c" add { id: "c" } } members_order: "c" members_order: "a" members_order: "b"`,
			`members { id: "c" } members { id: "a" } members { id: "b" }`,
		},
		{
			"a new oneof case starts from empty",
			`card { holder: "h" }`,
			`card { limit { units: 1 } }`,
			`card { holder: "h" limit { units: 1 } }`,
		},
		{
			"a different oneof case replaces the oneof",
			`cash: "c"`,
			`card { holder: "h" }`,
			`card { holder: "h" }`,
		},
		{
			"patching an unset message starts it from empty",
			``,
			`settings { theme: "t" }`,
			`settings { theme: "t" }`,
		},
		{
			"clearing a oneof case's field clears the case",
			`cash: "c"`,
			`unset: 23`,
			``,
		},
		{
			"a map with message values is patched by key",
			`configs { key: 1 value { theme: "t" } }`,
			`configs { key: 1 patch { color: COLOR_RED } } configs { key: 2 add { theme: "u" } }`,
			`configs { key: 1 value { theme: "t" color: COLOR_RED } } configs { key: 2 value { theme: "u" } }`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, d := rootOf(t, tt.a), diffOf(t, tt.d)
			got, err := Apply(a, d)
			if err != nil {
				t.Fatal(err)
			}
			if want := rootOf(t, tt.want); !proto.Equal(got, want) {
				t.Fatalf("got %v, want %v", prototext.Format(got), prototext.Format(want))
			}
			if !proto.Equal(a, rootOf(t, tt.a)) {
				t.Fatal("Apply changed a")
			}
		})
	}
}

func TestDiff(t *testing.T) {
	tests := []struct{ name, a, b, want string }{
		{
			"removed then added elements pair up as patches, and extra adds insert before the next kept element",
			`scores: 1 scores: 2 scores: 3 scores: 4`,
			`scores: 1 scores: 7 scores: 8 scores: 9 scores: 4`,
			`scores { index: 1 patch: 7 } scores { index: 2 patch: 8 } scores { index: 3 insert_before { items: 9 } }`,
		},
		{
			"extra removes stay removes, and adds at the end insert at n",
			`scores: 1 scores: 2 scores: 3`,
			`scores: 9 scores: 3 scores: 4`,
			`scores { index: 0 patch: 9 } scores { index: 1 remove {} } scores { index: 3 insert_before { items: 4 } }`,
		},
		{
			"keyed changes are in key order and the order is written only when it differs from the default",
			`members { id: "b" } members { id: "a" name: "A" }`,
			`members { id: "a" name: "B" } members { id: "c" }`,
			`members { id: "a" patch { name: "B" } } members { id: "b" remove {} } members { id: "c" add { id: "c" } }`,
		},
		{
			"a changed order is the complete final order",
			`members { id: "a" } members { id: "b" }`,
			`members { id: "b" } members { id: "a" }`,
			`members_order: "b" members_order: "a"`,
		},
		{
			"an UNORDERED list's order is not part of the diff",
			`groups { number: 1 } groups { number: 2 }`,
			`groups { number: 2 } groups { number: 1 }`,
			``,
		},
		{
			"an implicit field set to zero is set, not cleared",
			`name: "x" count: 1`,
			`count: 0`,
			`name: "" count: 0`,
		},
		{
			"an explicit field that becomes unset is cleared",
			`count: 1 weight: 1 settings {} created {}`,
			``,
			`unset: 2 unset: 4 unset: 10 unset: 12`,
		},
		{
			"a new oneof case is patched from empty",
			`cash: "c"`,
			`card {}`,
			`card {}`,
		},
		{
			"floats compare by bits",
			`ratio: nan weight: 0`,
			`ratio: nan weight: -0`,
			`weight: -0`,
		},
	}
	_, p := root(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dynamicpb.NewMessage(p.body)
			if err := Diff(rootOf(t, tt.a), rootOf(t, tt.b), got); err != nil {
				t.Fatal(err)
			}
			if want := diffOf(t, tt.want); !rules(nil).equal(got, want.ProtoReflect()) {
				t.Fatalf("got %v, want %v", prototext.Format(got), prototext.Format(want))
			}
		})
	}
}

func TestDiffRefusesBadInput(t *testing.T) {
	_, p := root(t)
	for _, tt := range []struct{ a, b, want string }{
		{``, `members { id: "a" } members { id: "a" }`, "b.members: two items have the key a"},
		{`blob: "x"`, ``, "a has unknown fields"},
	} {
		a := rootOf(t, tt.a)
		if tt.a != "" {
			addUnknown(a.ProtoReflect())
		}
		err := Diff(a, rootOf(t, tt.b), dynamicpb.NewMessage(p.body))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Diff(%s, %s) returned %v, want %q", tt.a, tt.b, err, tt.want)
		}
	}
	if err := Diff(rootOf(t, ``), parse(t, "pdgtest.v1.Member", ``), dynamicpb.NewMessage(p.body)); err == nil {
		t.Error("Diff accepted a and b of different types")
	}
	if err := Diff(parse(t, "pdgtest.v1.Member", ``), parse(t, "pdgtest.v1.Member", ``), dynamicpb.NewMessage(p.body)); err == nil {
		t.Error("Diff accepted a diff type for another target")
	}
	if err := Diff(rootOf(t, ``), rootOf(t, ``), rootOf(t, ``)); err == nil {
		t.Error("Diff accepted a non-diff out")
	}
}

func TestEqual(t *testing.T) {
	d := diffOf(t, ``)
	tests := []struct {
		x, y string
		want bool
	}{
		{`groups { number: 1 title: "a" } groups { number: 2 }`, `groups { number: 2 } groups { number: 1 title: "a" }`, true},
		{`groups { number: 1 title: "a" } groups { number: 2 }`, `groups { number: 2 } groups { number: 1 title: "b" }`, false},
		{`members { id: "a" } members { id: "b" }`, `members { id: "b" } members { id: "a" }`, false},
		{`groups { number: 1 seats { code: "x" } seats { code: "y" } }`, `groups { number: 1 seats { code: "y" } seats { code: "x" } }`, false},
		{`tree { children { id: "a" } children { id: "b" children { id: "c" } children { id: "d" } } }`, `tree { children { id: "b" children { id: "d" } children { id: "c" } } children { id: "a" } }`, true},
		{`ratio: nan`, `ratio: nan`, true},
		{`ratio: 0`, `ratio: -0`, false},
		{`count: 0`, ``, false},
		{`name: ""`, ``, true},
		{`scores: 1 scores: 2`, `scores: 2 scores: 1`, false},
		{`counts { key: "a" value: 1 } counts { key: "b" value: 2 }`, `counts { key: "b" value: 2 } counts { key: "a" value: 1 }`, true},
	}
	for _, tt := range tests {
		if got := Equal(rootOf(t, tt.x), rootOf(t, tt.y), d); got != tt.want {
			t.Errorf("Equal(%s, %s) = %v, want %v", tt.x, tt.y, got, tt.want)
		}
	}
}

func TestOverlap(t *testing.T) {
	tests := []struct {
		name, d1, d2 string
		want         []string
	}{
		{"different top-level fields", `name: "a"`, `count: 1`, nil},
		{"one top-level field", `name: "a"`, `name: "b"`, []string{"field 1"}},
		{"set and clear of one field", `count: 1`, `unset: 2`, []string{"field 2"}},
		{"different subfields of one singular message", `settings { theme: "t" }`, `settings { color: COLOR_RED }`, []string{"field 10"}},
		{"clearing a message overlaps changes inside it", `unset: 10`, `settings { theme: "t" }`, []string{"field 10"}},
		{"different fields of one keyed item", `members { id: "a" patch { name: "A" } }`, `members { id: "a" patch { color: COLOR_RED } }`, nil},
		{"one field of one keyed item", `members { id: "a" patch { name: "A" } }`, `members { id: "a" patch { name: "B" } }`, []string{"members[a] field 2"}},
		{"different keyed items", `members { id: "a" remove {} }`, `members { id: "b" remove {} }`, nil},
		{"a remove and a change inside the item", `members { id: "a" remove {} }`, `members { id: "a" patch { lines { index: 0 remove {} } } }`, []string{"members[a]"}},
		{"two adds of one key", `members { id: "c" add { id: "c" } }`, `members { id: "c" add { id: "c" name: "C" } }`, []string{"members[c]"}},
		{"nested keyed items", `groups { number: 1 patch { seats { code: "x" patch { row: 1 } } } }`, `groups { number: 1 patch { seats { code: "x" patch { row: 2 } } } }`, []string{"groups[1].seats[x] field 2"}},
		{"two orders", `members_order: "a"`, `members_order: "a"`, []string{"field 14"}},
		{"an order and an item change", `members_order: "a"`, `members { id: "a" patch { name: "A" } }`, nil},
		{"setting an atomic message", `budget { units: 1 }`, `budget { nanos: 1 }`, []string{"field 9"}},
		{"replacing a list", `tags { tags: "a" }`, `tags {}`, []string{"field 18"}},
		{"different cases of one oneof", `cash: "c"`, `card { holder: "h" }`, []string{"field 23"}},
		{"a map key", `counts { key: "k" set: 1 }`, `counts { key: "k" remove {} }`, []string{"counts[k]"}},
		{"patches of one indexed element", `lines { index: 1 patch { text: "a" } }`, `lines { index: 1 patch { qty: 1 } }`, []string{"lines[1]"}},
		{"inserts at one index", `scores { index: 2 insert_before { items: 1 } }`, `scores { index: 2 insert_before { items: 2 } }`, []string{"scores[2]"}},
		{"an insert and a remove at one index", `scores { index: 2 insert_before { items: 1 } }`, `scores { index: 2 remove {} }`, []string{"scores[2]"}},
		{"edits at different indexes", `scores { index: 1 remove {} }`, `scores { index: 2 insert_before { items: 1 } }`, nil},
		{"enum keys", `tagged { color: COLOR_RED remove {} }`, `tagged { color: COLOR_RED patch { note: "n" } }`, []string{"tagged[1]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := Overlap(diffOf(t, tt.d1), diffOf(t, tt.d2))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, it := range items {
				got = append(got, it.String())
			}
			if strings.Join(got, ";") != strings.Join(tt.want, ";") {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			back, err := Overlap(diffOf(t, tt.d2), diffOf(t, tt.d1))
			if err != nil || len(back) != len(items) {
				t.Fatalf("Overlap is not symmetric: %v, %v", back, err)
			}
		})
	}
	if _, err := Overlap(diffOf(t, ``), parse(t, "pdgtest.v1.diff.MemberDiff", ``)); err == nil {
		t.Error("Overlap accepted diffs of two types")
	}
}

func TestItem(t *testing.T) {
	items, err := Overlap(diffOf(t, `groups { number: 3 patch { seats { code: "x y" remove {} } } }`), diffOf(t, `groups { number: 3 patch { seats { code: "x y" patch {} } } }`))
	if err != nil || len(items) != 1 {
		t.Fatalf("got %v, %v", items, err)
	}
	it := items[0]
	path := it.Path()
	if len(path) != 2 || it.Field() != 0 {
		t.Fatalf("got %v", it)
	}
	if path[0].List() != "pdgtest.v1.Root.groups" || path[0].Key().Int() != 3 || path[0].Index() != -1 {
		t.Errorf("first step is %v", path[0])
	}
	if path[1].List() != "pdgtest.v1.Group.seats" || path[1].Key().String() != "x y" {
		t.Errorf("second step is %v", path[1])
	}
	if got, want := it.String(), `groups[3].seats["x y"]`; got != want {
		t.Errorf("String() = %s, want %s", got, want)
	}
	path[0] = Step{}
	if it.Path()[0].List() != "pdgtest.v1.Root.groups" {
		t.Error("Path() shares its slice with the item")
	}
	items, _ = Overlap(diffOf(t, `lines { index: 4 remove {} }`), diffOf(t, `lines { index: 4 remove {} }`))
	if s := items[0].Path()[0]; s.Index() != 4 || s.Key().IsValid() {
		t.Errorf("indexed step is %v", s)
	}
}

// TestMyers checks that the sequence diff is minimal, against an LCS table,
// and that its runs cover both sequences.
func TestMyers(t *testing.T) {
	r := rand.New(rand.NewPCG(4, 1))
	for c := 0; c < cases; c++ {
		a := make([]int, r.IntN(12))
		b := make([]int, r.IntN(12))
		for i := range a {
			a[i] = r.IntN(4)
		}
		for i := range b {
			b[i] = r.IntN(4)
		}
		runs := myers(len(a), len(b), func(i, j int) bool { return a[i] == b[j] })
		removed, added := 0, 0
		for _, rn := range runs {
			removed += len(rn.removed)
			added += len(rn.added)
			for _, i := range rn.removed {
				if i >= rn.next {
					t.Fatalf("%v -> %v: run %v removes past its next index", a, b, rn)
				}
			}
		}
		lcs := lcsLen(a, b)
		if removed != len(a)-lcs || added != len(b)-lcs {
			t.Fatalf("%v -> %v: %d removes and %d adds, want %d and %d", a, b, removed, added, len(a)-lcs, len(b)-lcs)
		}
	}
}

func lcsLen(a, b []int) int {
	t := make([][]int, len(a)+1)
	for i := range t {
		t[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	return t[0][0]
}
