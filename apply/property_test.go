package apply

import (
	"math/rand/v2"
	"sort"
	"sync"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/nexteggapp/protodiffgen/internal/testschema"
)

const cases = 3000

var (
	loadOnce sync.Once
	loaded   *testschema.Schema
	loadErr  error
)

// root returns the test schema's target and its patch body's plan.
func root(t *testing.T) (protoreflect.MessageDescriptor, *plan) {
	t.Helper()
	loadOnce.Do(func() { loaded, loadErr = testschema.Load() })
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	p, err := planFor(loaded.Message("pdgtest.v1.diff.RootDiff"))
	if err != nil {
		t.Fatal(err)
	}
	return loaded.Message("pdgtest.v1.Root"), p
}

func newGen(p *plan, seed uint64) *rgen {
	return &rgen{r: rand.New(rand.NewPCG(seed, 1)), rules: rulesFor(p.body)}
}

func text(m protoreflect.Message) string { return prototext.Format(m.Interface()) }

// TestRoundTrip checks Equal(Apply(a, Diff(a, b)), b), and that Diff's output
// is in normal form.
func TestRoundTrip(t *testing.T) {
	md, p := root(t)
	g := newGen(p, 1)
	for i := 0; i < cases; i++ {
		a := g.message(md, 4)
		var b protoreflect.Message
		if g.chance(0.3) {
			b = g.message(md, 4)
		} else {
			b = proto.Clone(a.Interface()).ProtoReflect()
			g.mutate(b, 4)
		}
		d := dynamicpb.NewMessage(p.body)
		if err := Diff(a.Interface(), b.Interface(), d); err != nil {
			t.Fatalf("case %d: Diff: %v\na: %s\nb: %s", i, err, text(a), text(b))
		}
		got, err := Apply(a.Interface(), d)
		if err != nil {
			t.Fatalf("case %d: Apply: %v\na: %s\nd: %s", i, err, text(a), text(d))
		}
		if !Equal(got, b.Interface(), d) {
			rest := dynamicpb.NewMessage(p.body)
			_ = Diff(got, b.Interface(), rest)
			t.Fatalf("case %d: Apply(a, Diff(a, b)) != b\na: %s\nb: %s\nd: %s\nleft over: %s", i, text(a), text(b), text(d), text(rest))
		}
		if n := normalise(p, a, d); !rules(nil).equal(n, d) {
			t.Fatalf("case %d: Diff is not in normal form\na: %s\nd: %s\nnormal: %s", i, text(a), text(d), text(n))
		}
		if Equal(a.Interface(), b.Interface(), d) != isEmpty(d) {
			t.Fatalf("case %d: Equal(a, b) is %v but the diff is %s", i, Equal(a.Interface(), b.Interface(), d), text(d))
		}
	}
}

// TestNormalForm checks that Diff(a, Apply(a, d)) is d in normal form, for
// random diffs that Validate accepts.
func TestNormalForm(t *testing.T) {
	md, p := root(t)
	g := newGen(p, 2)
	for i := 0; i < cases; i++ {
		a := g.message(md, 4)
		d := g.diff(p, a, 4)
		if err := Validate(a.Interface(), d.Interface()); err != nil {
			t.Fatalf("case %d: Validate: %v\na: %s\nd: %s", i, err, text(a), text(d))
		}
		b, err := Apply(a.Interface(), d.Interface())
		if err != nil {
			t.Fatal(err)
		}
		got := dynamicpb.NewMessage(p.body)
		if err := Diff(a.Interface(), b, got); err != nil {
			t.Fatalf("case %d: Diff: %v", i, err)
		}
		want := normalise(p, a, d)
		if !rules(nil).equal(got, want) {
			t.Fatalf("case %d: Diff(a, Apply(a, d)) is not d's normal form\na: %s\nd: %s\ngot: %s\nwant: %s", i, text(a), text(d), text(got), text(want))
		}
	}
}

// normalise returns d in its normal form for a: entries that change nothing
// in a are dropped, keyed list changes are in key order, an order field is
// kept only when it differs from the default order, and indexed edits are the
// canonical edits between the list and its result.
func normalise(p *plan, a, d protoreflect.Message) protoreflect.Message {
	r := rulesFor(p.body)
	out := dynamicpb.NewMessage(p.body)
	fields := a.Descriptor().Fields()
	cleared := map[protoreflect.FieldNumber]bool{}
	cl := d.Get(p.clear).List()
	for i := 0; i < cl.Len(); i++ {
		cleared[protoreflect.FieldNumber(cl.Get(i).Int())] = true
	}
	var clears []int32
	for _, fp := range p.fields {
		tfd := fields.ByNumber(fp.num)
		oneof := tfd.ContainingOneof()
		if oneof != nil && oneof.IsSynthetic() {
			oneof = nil
		}
		has := a.Has(tfd)
		if oneof != nil && a.WhichOneof(oneof) != tfd {
			has = false
		}
		if cleared[fp.num] {
			otherSet := false
			if oneof != nil {
				for _, other := range p.fields {
					ofd := fields.ByNumber(other.num)
					if ofd != tfd && ofd.ContainingOneof() == oneof && d.Has(other.diff) {
						otherSet = true
					}
				}
			}
			if has && !otherSet {
				clears = append(clears, int32(fp.num))
			}
			continue
		}
		if !fp.present(d) {
			continue
		}
		v := d.Get(fp.diff)
		switch fp.kind {
		case kScalar:
			if tfd.HasPresence() {
				if !has || !r.equalValue(tfd, a.Get(tfd), v) {
					out.Set(fp.diff, v)
				}
			} else if !r.equalValue(tfd, a.Get(tfd), v) {
				out.Set(fp.diff, v)
			}
		case kAtomic:
			if !has || !r.equal(a.Get(tfd).Message(), v.Message()) {
				out.Set(fp.diff, v)
			}
		case kPatch:
			base := a.Get(tfd).Message()
			if !has {
				base = dynamicpb.NewMessage(tfd.Message())
			}
			n := normalise(fp.sub, base, v.Message())
			if !has || !isEmpty(n) {
				out.Set(fp.diff, protoreflect.ValueOfMessage(n))
			}
		case kKeyed:
			if tfd.IsMap() {
				normaliseMap(r, fp, tfd, a.Get(tfd).Map(), v.List(), out)
			} else {
				normaliseKeyed(r, fp, tfd, a.Get(tfd).List(), d, out)
			}
		case kIndexed:
			// The canonical edits come from the list and its result, so this
			// uses the package's own walk and sequence diff.
			res := proto.Clone(a.Interface()).ProtoReflect()
			applyIndexed(fp, res.Mutable(tfd).List(), v.List())
			df := differ{rules: r}
			if err := df.indexed(fp, tfd, a.Get(tfd).List(), res.Get(tfd).List(), out); err != nil {
				panic(err)
			}
		case kReplace:
			if !r.equalList(tfd, a.Get(tfd).List(), v.Message().Get(fp.items).List()) {
				out.Set(fp.diff, v)
			}
		}
	}
	if len(clears) > 0 {
		sort.Slice(clears, func(i, j int) bool { return clears[i] < clears[j] })
		l := out.Mutable(p.clear).List()
		for _, n := range clears {
			l.Append(protoreflect.ValueOfInt32(n))
		}
	}
	return out
}

func normaliseKeyed(r rules, fp *fplan, tfd protoreflect.FieldDescriptor, a protoreflect.List, d, out protoreflect.Message) {
	keyFd := tfd.Message().Fields().ByName(fp.keySrc)
	have, _ := indexKeys(a, keyFd)
	var keep []protoreflect.Message
	removed := map[any]bool{}
	var added []protoreflect.MapKey
	l := d.Get(fp.diff).List()
	for i := 0; i < l.Len(); i++ {
		w := l.Get(i).Message()
		k := keyOf(fp.key, w.Get(fp.key))
		switch op := w.WhichOneof(fp.op); op {
		case fp.remove:
			removed[k.Interface()] = true
			keep = append(keep, w)
		case fp.add:
			added = append(added, k)
			keep = append(keep, w)
		case fp.patch:
			item := a.Get(have[k.Interface()]).Message()
			if fp.sub == nil {
				if !r.equal(item, w.Get(op).Message()) {
					keep = append(keep, w)
				}
				continue
			}
			n := normalise(fp.sub, item, w.Get(op).Message())
			if !isEmpty(n) {
				nw := dynamicpb.NewMessage(fp.wrap)
				nw.Set(fp.key, w.Get(fp.key))
				nw.Set(fp.patch, protoreflect.ValueOfMessage(n))
				keep = append(keep, nw)
			}
		}
	}
	sort.Slice(keep, func(i, j int) bool {
		return lessKey(keyOf(fp.key, keep[i].Get(fp.key)), keyOf(fp.key, keep[j].Get(fp.key)))
	})
	if len(keep) > 0 {
		ol := out.Mutable(fp.diff).List()
		for _, w := range keep {
			ol.Append(protoreflect.ValueOfMessage(w))
		}
	}
	if fp.order == nil || !d.Has(fp.order) {
		return
	}
	sort.Slice(added, func(i, j int) bool { return lessKey(added[i], added[j]) })
	var def []any
	for i := 0; i < a.Len(); i++ {
		k := keyOf(keyFd, a.Get(i).Message().Get(keyFd)).Interface()
		if !removed[k] {
			def = append(def, k)
		}
	}
	for _, k := range added {
		def = append(def, k.Interface())
	}
	order := d.Get(fp.order).List()
	for i := 0; i < order.Len(); i++ {
		if keyOf(fp.order, order.Get(i)).Interface() != def[i] {
			out.Set(fp.order, d.Get(fp.order))
			return
		}
	}
}

func normaliseMap(r rules, fp *fplan, tfd protoreflect.FieldDescriptor, a protoreflect.Map, l protoreflect.List, out protoreflect.Message) {
	var keep []protoreflect.Message
	for i := 0; i < l.Len(); i++ {
		w := l.Get(i).Message()
		k := w.Get(fp.key).MapKey()
		switch op := w.WhichOneof(fp.op); op {
		case fp.remove, fp.add:
			keep = append(keep, w)
		case fp.set:
			if !a.Has(k) || !r.equalValue(tfd.MapValue(), a.Get(k), w.Get(op)) {
				keep = append(keep, w)
			}
		case fp.patch:
			n := normalise(fp.sub, a.Get(k).Message(), w.Get(op).Message())
			if !isEmpty(n) {
				nw := dynamicpb.NewMessage(fp.wrap)
				nw.Set(fp.key, w.Get(fp.key))
				nw.Set(fp.patch, protoreflect.ValueOfMessage(n))
				keep = append(keep, nw)
			}
		}
	}
	sort.Slice(keep, func(i, j int) bool { return lessKey(keep[i].Get(fp.key).MapKey(), keep[j].Get(fp.key).MapKey()) })
	if len(keep) > 0 {
		ol := out.Mutable(fp.diff).List()
		for _, w := range keep {
			ol.Append(protoreflect.ValueOfMessage(w))
		}
	}
}
