package apply

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// A fault turns a valid diff into one Validate must refuse. It reports false
// when the message at hand has no place for it.
type fault struct {
	name   string
	want   string // part of the error Validate returns
	inject func(g *rgen, p *plan, a, d protoreflect.Message) bool
}

// TestValidateRefuses injects one fault from each case Validate refuses into
// random valid diffs, at random depths, and checks that Validate refuses it
// for that reason.
func TestValidateRefuses(t *testing.T) {
	md, p := root(t)
	g := newGen(p, 3)
	hits := map[string]int{}
	for i := 0; i < 2*cases; i++ {
		a := g.message(md, 4)
		d := g.diff(p, a, 3)
		f := faults[i%len(faults)]
		if f.name == "unknown fields in a" {
			if !injectUnknown(g, a) {
				continue
			}
		} else if !g.inject(f, p, a, d, 2) {
			continue
		}
		hits[f.name]++
		err := Validate(a.Interface(), d.Interface())
		if err == nil || !strings.Contains(err.Error(), f.want) {
			t.Fatalf("case %d, %s: Validate returned %v, want an error containing %q\na: %s\nd: %s", i, f.name, err, f.want, text(a), text(d))
		}
		if _, err := Apply(a.Interface(), d.Interface()); err == nil {
			t.Fatalf("case %d, %s: Apply accepted the diff", i, f.name)
		}
	}
	for _, f := range faults {
		if hits[f.name] < 50 {
			t.Errorf("%s was injected only %d times", f.name, hits[f.name])
		}
	}
	t.Logf("injected: %v", hits)
}

// inject puts f into d, here or, by chance or for want of a place here, in a
// patch below.
func (g *rgen) inject(f fault, p *plan, a, d protoreflect.Message, depth int) bool {
	if !(depth > 0 && g.chance(0.5)) && f.inject(g, p, a, d) {
		return true
	}
	for t := 0; depth > 0 && t < 3; t++ {
		saved := proto.Clone(d.Interface())
		if sp, sa, sd := g.descend(p, a, d); sp != nil && g.inject(f, sp, sa, sd, depth-1) {
			return true
		}
		proto.Reset(d.Interface())
		proto.Merge(d.Interface(), saved)
	}
	return false
}

// descend returns a patch inside d, made if need be, with its plan and base.
func (g *rgen) descend(p *plan, a, d protoreflect.Message) (*plan, protoreflect.Message, protoreflect.Message) {
	fields := a.Descriptor().Fields()
	var choices []*fplan
	for _, fp := range p.fields {
		tfd := fields.ByNumber(fp.num)
		switch {
		case fp.kind == kPatch && tfd.ContainingOneof() == nil:
			choices = append(choices, fp)
		case fp.kind == kKeyed && !tfd.IsMap() && fp.sub != nil && a.Get(tfd).List().Len() > 0:
			choices = append(choices, fp)
		}
	}
	if len(choices) == 0 {
		return nil, nil, nil
	}
	fp := choices[g.pick(len(choices))]
	tfd := fields.ByNumber(fp.num)
	if fp.kind == kPatch {
		dropClear(p, d, fp.num)
		base := a.Get(tfd).Message()
		if !a.Has(tfd) {
			base = dynamicpb.NewMessage(tfd.Message())
		}
		return fp.sub, base, d.Mutable(fp.diff).Message()
	}
	l := a.Get(tfd).List()
	item := l.Get(g.pick(l.Len())).Message()
	k := item.Get(tfd.Message().Fields().ByName(fp.keySrc))
	dropKey(fp, d, k)
	w := d.Mutable(fp.diff).List().NewElement().Message()
	w.Set(fp.key, k)
	patch := w.Mutable(fp.patch).Message()
	d.Mutable(fp.diff).List().Append(protoreflect.ValueOfMessage(w))
	return fp.sub, item, patch
}

func dropClear(p *plan, d protoreflect.Message, num protoreflect.FieldNumber) {
	l := d.Get(p.clear).List()
	var keep []protoreflect.Value
	for i := 0; i < l.Len(); i++ {
		if protoreflect.FieldNumber(l.Get(i).Int()) != num {
			keep = append(keep, l.Get(i))
		}
	}
	d.Clear(p.clear)
	for _, v := range keep {
		d.Mutable(p.clear).List().Append(v)
	}
}

// dropKey removes d's changes for key k.
func dropKey(fp *fplan, d protoreflect.Message, k protoreflect.Value) {
	l := d.Get(fp.diff).List()
	var keep []protoreflect.Value
	for i := 0; i < l.Len(); i++ {
		if keyOf(fp.key, l.Get(i).Message().Get(fp.key)).Interface() != keyOf(fp.key, k).Interface() {
			keep = append(keep, l.Get(i))
		}
	}
	d.Clear(fp.diff)
	for _, v := range keep {
		d.Mutable(fp.diff).List().Append(v)
	}
}

// keyedFields returns the keyed lists and maps of p, with their target fields.
func keyedFields(p *plan, a protoreflect.Message, ok func(fp *fplan, tfd protoreflect.FieldDescriptor) bool) []*fplan {
	var out []*fplan
	for _, fp := range p.fields {
		tfd := a.Descriptor().Fields().ByNumber(fp.num)
		if fp.kind == kKeyed && ok(fp, tfd) {
			out = append(out, fp)
		}
	}
	return out
}

func (g *rgen) choose(fps []*fplan) *fplan {
	if len(fps) == 0 {
		return nil
	}
	return fps[g.pick(len(fps))]
}

// keys returns the keys a target list or map has.
func keys(fp *fplan, tfd protoreflect.FieldDescriptor, a protoreflect.Message) []protoreflect.Value {
	var out []protoreflect.Value
	if tfd.IsMap() {
		a.Get(tfd).Map().Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
			out = append(out, k.Value())
			return true
		})
		return out
	}
	keyFd := tfd.Message().Fields().ByName(fp.keySrc)
	l := a.Get(tfd).List()
	for i := 0; i < l.Len(); i++ {
		out = append(out, l.Get(i).Message().Get(keyFd))
	}
	return out
}

// freshKey returns a key that neither a nor d uses.
func (g *rgen) freshKey(fp *fplan, tfd protoreflect.FieldDescriptor, a, d protoreflect.Message) (protoreflect.Value, bool) {
	used := map[any]bool{}
	for _, k := range keys(fp, tfd, a) {
		used[keyOf(fp.key, k).Interface()] = true
	}
	l := d.Get(fp.diff).List()
	for i := 0; i < l.Len(); i++ {
		used[keyOf(fp.key, l.Get(i).Message().Get(fp.key)).Interface()] = true
	}
	for t := 0; t < 20; t++ {
		k := g.key(fp.key)
		if !used[keyOf(fp.key, k).Interface()] {
			return k, true
		}
	}
	return protoreflect.Value{}, false
}

func appendChange(fp *fplan, d protoreflect.Message, k protoreflect.Value, op protoreflect.FieldDescriptor, v protoreflect.Value) protoreflect.Message {
	l := d.Mutable(fp.diff).List()
	w := l.NewElement().Message()
	w.Set(fp.key, k)
	if op != nil {
		if !v.IsValid() {
			v = w.NewField(op)
		}
		w.Set(op, v)
	}
	l.Append(protoreflect.ValueOfMessage(w))
	return w
}

func appendEdit(fp *fplan, d protoreflect.Message, idx int, op protoreflect.FieldDescriptor) protoreflect.Message {
	l := d.Mutable(fp.diff).List()
	e := l.NewElement().Message()
	e.Set(fp.index, protoreflect.ValueOfUint32(uint32(idx)))
	if op != nil {
		e.Set(op, e.NewField(op))
	}
	l.Append(protoreflect.ValueOfMessage(e))
	return e
}

func indexedFields(p *plan) []*fplan {
	var out []*fplan
	for _, fp := range p.fields {
		if fp.kind == kIndexed {
			out = append(out, fp)
		}
	}
	return out
}

// edits returns which indexes d inserts at and patches or removes.
func edits(fp *fplan, d protoreflect.Message) (inserts, changes map[int]bool) {
	inserts, changes = map[int]bool{}, map[int]bool{}
	l := d.Get(fp.diff).List()
	for i := 0; i < l.Len(); i++ {
		e := l.Get(i).Message()
		idx := int(e.Get(fp.index).Uint())
		if e.WhichOneof(fp.op) == fp.insert {
			inserts[idx] = true
		} else {
			changes[idx] = true
		}
	}
	return inserts, changes
}

func injectUnknown(g *rgen, m protoreflect.Message) bool {
	b := protowire.AppendTag(nil, 9999, protowire.VarintType)
	b = protowire.AppendVarint(b, 1)
	target := m
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Message() != nil && !fd.IsList() && !fd.IsMap() && g.chance(0.3) {
			target = v.Message()
			return false
		}
		return true
	})
	target.SetUnknown(b)
	return true
}

var faults = []fault{
	{"adds a key the list has", "adds a key the", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(keyedFields(p, a, func(fp *fplan, tfd protoreflect.FieldDescriptor) bool {
			return fp.add != nil && len(keys(fp, tfd, a)) > 0
		}))
		if fp == nil {
			return false
		}
		tfd := a.Descriptor().Fields().ByNumber(fp.num)
		ks := keys(fp, tfd, a)
		k := ks[g.pick(len(ks))]
		dropKey(fp, d, k)
		var item protoreflect.Value
		if tfd.IsMap() {
			item = protoreflect.ValueOfMessage(proto.Clone(a.Get(tfd).Map().Get(k.MapKey()).Message().Interface()).ProtoReflect())
		} else {
			item = protoreflect.ValueOfMessage(dynamicpb.NewMessage(tfd.Message()))
			item.Message().Set(tfd.Message().Fields().ByName(fp.keySrc), k)
		}
		appendChange(fp, d, k, fp.add, item)
		return true
	}},
	{"removes a key the list lacks", "removes a key the", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(keyedFields(p, a, func(*fplan, protoreflect.FieldDescriptor) bool { return true }))
		if fp == nil {
			return false
		}
		k, ok := g.freshKey(fp, a.Descriptor().Fields().ByNumber(fp.num), a, d)
		if ok {
			appendChange(fp, d, k, fp.remove, protoreflect.Value{})
		}
		return ok
	}},
	{"patches a key the list lacks", "patches a key the", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(keyedFields(p, a, func(fp *fplan, _ protoreflect.FieldDescriptor) bool { return fp.patch != nil }))
		if fp == nil {
			return false
		}
		tfd := a.Descriptor().Fields().ByNumber(fp.num)
		k, ok := g.freshKey(fp, tfd, a, d)
		if !ok {
			return false
		}
		w := appendChange(fp, d, k, fp.patch, protoreflect.Value{})
		if fp.sub == nil {
			w.Get(fp.patch).Message().Set(tfd.Message().Fields().ByName(fp.keySrc), k)
		}
		return true
	}},
	{"two entries for one key", "two changes for one key", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(keyedFields(p, a, func(fp *fplan, _ protoreflect.FieldDescriptor) bool { return d.Get(fp.diff).List().Len() > 0 }))
		if fp == nil {
			return false
		}
		l := d.Mutable(fp.diff).List()
		l.Append(protoreflect.ValueOfMessage(proto.Clone(l.Get(g.pick(l.Len())).Message().Interface()).ProtoReflect()))
		return true
	}},
	{"a change wrapper with no op", "with no op", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(keyedFields(p, a, func(*fplan, protoreflect.FieldDescriptor) bool { return true }))
		if fp == nil {
			return false
		}
		k, ok := g.freshKey(fp, a.Descriptor().Fields().ByNumber(fp.num), a, d)
		if ok {
			appendChange(fp, d, k, nil, protoreflect.Value{})
		}
		return ok
	}},
	{"an indexed edit with no op", "with no op", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(indexedFields(p))
		if fp == nil {
			return false
		}
		appendEdit(fp, d, g.pick(3), nil)
		return true
	}},
	{"a wrapper key that differs from its add's key", "the add's key is", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(keyedFields(p, a, func(_ *fplan, tfd protoreflect.FieldDescriptor) bool { return !tfd.IsMap() }))
		if fp == nil {
			return false
		}
		tfd := a.Descriptor().Fields().ByNumber(fp.num)
		k, ok := g.freshKey(fp, tfd, a, d)
		if !ok {
			return false
		}
		keyFd := tfd.Message().Fields().ByName(fp.keySrc)
		for t := 0; t < 20; t++ {
			other := g.key(keyFd)
			if keyOf(keyFd, other).Interface() != keyOf(keyFd, k).Interface() {
				item := dynamicpb.NewMessage(tfd.Message())
				item.Set(keyFd, other)
				appendChange(fp, d, k, fp.add, protoreflect.ValueOfMessage(item))
				return true
			}
		}
		return false
	}},
	{"a patch that changes a keyed item's key", "the patch changes the key", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(keyedFields(p, a, func(fp *fplan, tfd protoreflect.FieldDescriptor) bool {
			return !tfd.IsMap() && a.Get(tfd).List().Len() > 0
		}))
		if fp == nil {
			return false
		}
		tfd := a.Descriptor().Fields().ByNumber(fp.num)
		keyFd := tfd.Message().Fields().ByName(fp.keySrc)
		ks := keys(fp, tfd, a)
		k := ks[g.pick(len(ks))]
		for t := 0; t < 20; t++ {
			other := g.key(keyFd)
			if keyOf(keyFd, other).Interface() == keyOf(keyFd, k).Interface() {
				continue
			}
			dropKey(fp, d, k)
			w := appendChange(fp, d, k, fp.patch, protoreflect.Value{})
			patch := w.Mutable(fp.patch).Message()
			if fp.sub == nil {
				patch.Set(keyFd, other)
			} else {
				patch.Set(fp.sub.byNum[keyFd.Number()].diff, other)
			}
			return true
		}
		return false
	}},
	{"clears a field without explicit presence", "no explicit presence", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		var nums []protoreflect.FieldNumber
		for _, fp := range p.fields {
			if !a.Descriptor().Fields().ByNumber(fp.num).HasPresence() {
				nums = append(nums, fp.num)
			}
		}
		if len(nums) == 0 {
			return false
		}
		d.Mutable(p.clear).List().Append(protoreflect.ValueOfInt32(int32(nums[g.pick(len(nums))])))
		return true
	}},
	{"both sets and clears one field", "both sets and clears", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		var fps []*fplan
		for _, fp := range p.fields {
			if a.Descriptor().Fields().ByNumber(fp.num).HasPresence() && fp.kind == kScalar {
				fps = append(fps, fp)
			}
		}
		fp := g.choose(fps)
		if fp == nil {
			return false
		}
		// Unset any other case first, so the diff still sets one case at most.
		if o := fp.diff.ContainingOneof(); o != nil {
			if f := d.WhichOneof(o); f != nil {
				d.Clear(f)
			}
		}
		d.Set(fp.diff, g.scalar(a.Descriptor().Fields().ByNumber(fp.num)))
		d.Mutable(p.clear).List().Append(protoreflect.ValueOfInt32(int32(fp.num)))
		return true
	}},
	{"an order that doesn't name exactly the resulting keys", "the order doesn't name exactly", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(keyedFields(p, a, func(fp *fplan, _ protoreflect.FieldDescriptor) bool { return fp.order != nil }))
		if fp == nil {
			return false
		}
		tfd := a.Descriptor().Fields().ByNumber(fp.num)
		result := map[any]protoreflect.Value{}
		for _, k := range keys(fp, tfd, a) {
			result[keyOf(fp.key, k).Interface()] = k
		}
		l := d.Get(fp.diff).List()
		for i := 0; i < l.Len(); i++ {
			w := l.Get(i).Message()
			k := w.Get(fp.key)
			switch w.WhichOneof(fp.op) {
			case fp.add:
				result[keyOf(fp.key, k).Interface()] = k
			case fp.remove:
				delete(result, keyOf(fp.key, k).Interface())
			}
		}
		var order []protoreflect.Value
		for _, k := range result {
			order = append(order, k)
		}
		switch {
		case len(order) > 0 && g.chance(0.5):
			if g.chance(0.5) {
				order = order[1:]
			} else {
				order = append(order, order[0])
			}
		default:
			k, ok := g.freshKey(fp, tfd, a, d)
			if !ok {
				return false
			}
			order = append(order, k)
		}
		if len(order) == 0 {
			return false
		}
		d.Clear(fp.order)
		ol := d.Mutable(fp.order).List()
		for _, k := range order {
			ol.Append(k)
		}
		return true
	}},
	{"a patch or remove index outside [0, n)", "is outside [0, ", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(indexedFields(p))
		if fp == nil {
			return false
		}
		n := a.Get(a.Descriptor().Fields().ByNumber(fp.num)).List().Len()
		op := fp.remove
		if g.chance(0.5) {
			op = fp.patch
		}
		appendEdit(fp, d, n+g.pick(2), op)
		return true
	}},
	{"an insert index outside [0, n]", "insert index", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(indexedFields(p))
		if fp == nil {
			return false
		}
		n := a.Get(a.Descriptor().Fields().ByNumber(fp.num)).List().Len()
		e := appendEdit(fp, d, n+1+g.pick(2), fp.insert)
		tfd := a.Descriptor().Fields().ByNumber(fp.num)
		e.Mutable(fp.insert).Message().Mutable(fp.items).List().Append(g.element(tfd, 1))
		return true
	}},
	{"a second insert at one index", "a second insert", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(indexedFields(p))
		if fp == nil {
			return false
		}
		tfd := a.Descriptor().Fields().ByNumber(fp.num)
		n := a.Get(tfd).List().Len()
		idx := g.pick(n + 1)
		inserts, _ := edits(fp, d)
		for c := 0; c < 2; c++ {
			if c == 0 && inserts[idx] {
				continue
			}
			e := appendEdit(fp, d, idx, fp.insert)
			e.Mutable(fp.insert).Message().Mutable(fp.items).List().Append(g.element(tfd, 1))
		}
		return true
	}},
	{"two of patch and remove at one index", "two of patch and remove", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(indexedFields(p))
		if fp == nil {
			return false
		}
		n := a.Get(a.Descriptor().Fields().ByNumber(fp.num)).List().Len()
		if n == 0 {
			return false
		}
		idx := g.pick(n)
		_, changes := edits(fp, d)
		if !changes[idx] {
			appendEdit(fp, d, idx, fp.remove)
		}
		appendEdit(fp, d, idx, fp.remove)
		return true
	}},
	{"an insert with no elements", "an insert with no elements", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(indexedFields(p))
		if fp == nil {
			return false
		}
		n := a.Get(a.Descriptor().Fields().ByNumber(fp.num)).List().Len()
		inserts, _ := edits(fp, d)
		idx := g.pick(n + 1)
		if inserts[idx] {
			return false
		}
		appendEdit(fp, d, idx, fp.insert)
		return true
	}},
	{"an optional key unset in an add", "leaves the key", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		fp := g.choose(keyedFields(p, a, func(fp *fplan, tfd protoreflect.FieldDescriptor) bool {
			return !tfd.IsMap() && tfd.Message().Fields().ByName(fp.keySrc).HasPresence()
		}))
		if fp == nil {
			return false
		}
		tfd := a.Descriptor().Fields().ByNumber(fp.num)
		k, ok := g.freshKey(fp, tfd, a, d)
		if ok {
			appendChange(fp, d, k, fp.add, protoreflect.ValueOfMessage(dynamicpb.NewMessage(tfd.Message())))
		}
		return ok
	}},
	{"unknown fields in d", "has unknown fields", func(g *rgen, p *plan, a, d protoreflect.Message) bool {
		return injectUnknown(g, d)
	}},
	{"unknown fields in a", "has unknown fields", nil},
}
