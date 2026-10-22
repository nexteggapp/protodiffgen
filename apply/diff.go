// Package apply computes, applies and checks protodiffgen diffs by
// reflection. It needs no generated Go of its own: the caller passes its own
// generated target and diff types, or dynamic messages.
package apply

import (
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Diff writes into out, an empty diff message, the smallest diff from a to b.
// out is reset first. The diff is in normal form: it holds no entry that
// changes nothing, keyed list changes are in key order, and indexed edits come
// from a Myers sequence diff, in index order.
func Diff(a, b, out proto.Message) error {
	am, bm, om := a.ProtoReflect(), b.ProtoReflect(), out.ProtoReflect()
	p, err := planFor(om.Descriptor())
	if err != nil {
		return err
	}
	if err := p.bind(am.Descriptor()); err != nil {
		return err
	}
	if bm.Descriptor().FullName() != am.Descriptor().FullName() {
		return fmt.Errorf("a is %s but b is %s", am.Descriptor().FullName(), bm.Descriptor().FullName())
	}
	if err := p.bind(bm.Descriptor()); err != nil {
		return err
	}
	if err := checkData(p, am, "a"); err != nil {
		return err
	}
	if err := checkData(p, bm, "b"); err != nil {
		return err
	}
	proto.Reset(out)
	d := differ{rules: rulesFor(om.Descriptor())}
	return d.message(p, am, bm, om)
}

type differ struct {
	rules rules
}

// message writes into out the patch from a to b. a may be an empty message.
func (df *differ) message(p *plan, a, b, out protoreflect.Message) error {
	if err := p.bind(a.Descriptor()); err != nil {
		return err
	}
	var clears []int32
	fields := a.Descriptor().Fields()
	for _, fp := range p.fields {
		tfd := fields.ByNumber(fp.num)
		bfd := b.Descriptor().Fields().ByNumber(fp.num)
		ha, hb := a.Has(tfd), b.Has(bfd)
		if o := tfd.ContainingOneof(); o != nil && !o.IsSynthetic() {
			ca := a.WhichOneof(o)
			cb := b.WhichOneof(b.Descriptor().Oneofs().ByName(o.Name()))
			if cb == nil || cb.Number() != fp.num {
				if ha && cb == nil {
					clears = append(clears, int32(fp.num))
				}
				continue
			}
			if ca == nil || ca.Number() != fp.num {
				// A new case starts from empty.
				ha = false
			}
		}
		switch fp.kind {
		case kScalar:
			av, bv := a.Get(tfd), b.Get(bfd)
			if tfd.HasPresence() {
				if ha && !hb {
					clears = append(clears, int32(fp.num))
				} else if hb && (!ha || !df.rules.equalValue(tfd, av, bv)) {
					out.Set(fp.diff, bv)
				}
			} else if !df.rules.equalValue(tfd, av, bv) {
				out.Set(fp.diff, bv)
			}
		case kAtomic:
			if ha && !hb {
				clears = append(clears, int32(fp.num))
			} else if hb && (!ha || !df.rules.equal(a.Get(tfd).Message(), b.Get(bfd).Message())) {
				out.Set(fp.diff, convert(out, fp.diff, b.Get(bfd)))
			}
		case kPatch:
			if ha && !hb {
				clears = append(clears, int32(fp.num))
				continue
			}
			if !hb {
				continue
			}
			base := a.Get(tfd).Message()
			if !ha {
				base = b.Get(bfd).Message().Type().New()
			}
			sub := out.Mutable(fp.diff).Message()
			if err := df.message(fp.sub, base, b.Get(bfd).Message(), sub); err != nil {
				return err
			}
			if ha && isEmpty(sub) {
				out.Clear(fp.diff)
			}
		case kKeyed:
			var err error
			if tfd.IsMap() {
				err = df.mapField(fp, tfd, a.Get(tfd).Map(), b.Get(bfd).Map(), out)
			} else {
				err = df.keyed(fp, tfd, a.Get(tfd).List(), b.Get(bfd).List(), out)
			}
			if err != nil {
				return err
			}
		case kIndexed:
			if err := df.indexed(fp, tfd, a.Get(tfd).List(), b.Get(bfd).List(), out); err != nil {
				return err
			}
		case kReplace:
			if !df.rules.equalList(tfd, a.Get(tfd).List(), b.Get(bfd).List()) {
				w := out.Mutable(fp.diff).Message()
				dst := w.Mutable(fp.items).List()
				src := b.Get(bfd).List()
				for i := 0; i < src.Len(); i++ {
					dst.Append(convertElem(dst, src.Get(i)))
				}
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
	return nil
}

type keyChange struct {
	key protoreflect.MapKey
	op  protoreflect.FieldDescriptor
	val protoreflect.Value
}

func (df *differ) keyed(fp *fplan, tfd protoreflect.FieldDescriptor, a, b protoreflect.List, out protoreflect.Message) error {
	keyFd := tfd.Message().Fields().ByName(fp.keySrc)
	ai, _ := indexKeys(a, keyFd)
	bi, _ := indexKeys(b, keyFd)
	var changes []keyChange
	for i := 0; i < a.Len(); i++ {
		k := keyOf(keyFd, a.Get(i).Message().Get(keyFd))
		if _, ok := bi[k.Interface()]; !ok {
			changes = append(changes, keyChange{key: k, op: fp.remove})
		}
	}
	var added []protoreflect.MapKey
	for j := 0; j < b.Len(); j++ {
		item := b.Get(j).Message()
		k := keyOf(keyFd, item.Get(keyFd))
		i, ok := ai[k.Interface()]
		if !ok {
			changes = append(changes, keyChange{key: k, op: fp.add, val: b.Get(j)})
			added = append(added, k)
			continue
		}
		if fp.sub == nil {
			if !df.rules.equal(a.Get(i).Message(), item) {
				changes = append(changes, keyChange{key: k, op: fp.patch, val: b.Get(j)})
			}
			continue
		}
		patch := out.NewField(fp.diff).List().NewElement().Message().Mutable(fp.patch).Message()
		if err := df.message(fp.sub, a.Get(i).Message(), item, patch); err != nil {
			return err
		}
		if !isEmpty(patch) {
			changes = append(changes, keyChange{key: k, op: fp.patch, val: protoreflect.ValueOfMessage(patch)})
		}
	}
	df.emit(fp, changes, out)

	if fp.order != nil {
		sort.Slice(added, func(i, j int) bool { return lessKey(added[i], added[j]) })
		var def []protoreflect.MapKey
		for i := 0; i < a.Len(); i++ {
			k := keyOf(keyFd, a.Get(i).Message().Get(keyFd))
			if _, ok := bi[k.Interface()]; ok {
				def = append(def, k)
			}
		}
		def = append(def, added...)
		same := true
		for j := 0; j < b.Len(); j++ {
			k := keyOf(keyFd, b.Get(j).Message().Get(keyFd))
			if def[j].Interface() != k.Interface() {
				same = false
				break
			}
		}
		if !same {
			l := out.Mutable(fp.order).List()
			for j := 0; j < b.Len(); j++ {
				l.Append(b.Get(j).Message().Get(keyFd))
			}
		}
	}
	return nil
}

func (df *differ) mapField(fp *fplan, tfd protoreflect.FieldDescriptor, a, b protoreflect.Map, out protoreflect.Message) error {
	var changes []keyChange
	a.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
		if !b.Has(k) {
			changes = append(changes, keyChange{key: k, op: fp.remove})
		}
		return true
	})
	var err error
	b.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		switch {
		case fp.set != nil:
			if !a.Has(k) || !df.rules.equalValue(tfd.MapValue(), a.Get(k), v) {
				changes = append(changes, keyChange{key: k, op: fp.set, val: v})
			}
		case !a.Has(k):
			changes = append(changes, keyChange{key: k, op: fp.add, val: v})
		default:
			patch := out.NewField(fp.diff).List().NewElement().Message().Mutable(fp.patch).Message()
			if err = df.message(fp.sub, a.Get(k).Message(), v.Message(), patch); err != nil {
				return false
			}
			if !isEmpty(patch) {
				changes = append(changes, keyChange{key: k, op: fp.patch, val: protoreflect.ValueOfMessage(patch)})
			}
		}
		return true
	})
	if err != nil {
		return err
	}
	df.emit(fp, changes, out)
	return nil
}

// emit appends change wrappers to out in key order.
func (df *differ) emit(fp *fplan, changes []keyChange, out protoreflect.Message) {
	if len(changes) == 0 {
		return
	}
	sort.Slice(changes, func(i, j int) bool { return lessKey(changes[i].key, changes[j].key) })
	l := out.Mutable(fp.diff).List()
	for _, c := range changes {
		w := l.NewElement().Message()
		w.Set(fp.key, keyValue(fp.key, c.key))
		switch {
		case c.op == fp.remove:
			w.Set(c.op, w.NewField(c.op))
		case c.op.Message() != nil:
			w.Set(c.op, convert(w, c.op, c.val))
		default:
			w.Set(c.op, c.val)
		}
		l.Append(protoreflect.ValueOfMessage(w))
	}
}

func (df *differ) indexed(fp *fplan, tfd protoreflect.FieldDescriptor, a, b protoreflect.List, out protoreflect.Message) error {
	eq := func(i, j int) bool { return df.rules.equalValue(tfd, a.Get(i), b.Get(j)) }
	rs := myers(a.Len(), b.Len(), eq)
	if len(rs) == 0 {
		return nil
	}
	l := out.Mutable(fp.diff).List()
	edit := func(index int) protoreflect.Message {
		e := l.NewElement().Message()
		e.Set(fp.index, protoreflect.ValueOfUint32(uint32(index)))
		l.Append(protoreflect.ValueOfMessage(e))
		return e
	}
	for _, r := range rs {
		pairs := min(len(r.removed), len(r.added))
		for t, i := range r.removed {
			e := edit(i)
			if t >= pairs {
				e.Set(fp.remove, e.NewField(fp.remove))
				continue
			}
			nv := b.Get(r.added[t])
			switch {
			case fp.sub != nil:
				patch := e.Mutable(fp.patch).Message()
				if err := df.message(fp.sub, a.Get(i).Message(), nv.Message(), patch); err != nil {
					return err
				}
			case fp.patch.Message() != nil:
				e.Set(fp.patch, convert(e, fp.patch, nv))
			default:
				e.Set(fp.patch, nv)
			}
		}
		if len(r.added) > pairs {
			e := edit(r.next)
			items := e.Mutable(fp.insert).Message().Mutable(fp.items).List()
			for _, j := range r.added[pairs:] {
				items.Append(convertElem(items, b.Get(j)))
			}
		}
	}
	return nil
}

// isEmpty reports whether m has no field set.
func isEmpty(m protoreflect.Message) bool {
	empty := true
	m.Range(func(protoreflect.FieldDescriptor, protoreflect.Value) bool {
		empty = false
		return false
	})
	return empty
}

// convert returns v, a value for field fd of m, as a value m accepts: message
// values of another Go type or descriptor are copied through the wire format.
func convert(m protoreflect.Message, fd protoreflect.FieldDescriptor, v protoreflect.Value) protoreflect.Value {
	if fd.Message() == nil {
		return v
	}
	nv := m.NewField(fd)
	copyMessage(nv.Message(), v.Message())
	return nv
}

func convertElem(l protoreflect.List, v protoreflect.Value) protoreflect.Value {
	if _, ok := v.Interface().(protoreflect.Message); !ok {
		return v
	}
	nv := l.NewElement()
	copyMessage(nv.Message(), v.Message())
	return nv
}

func copyMessage(dst, src protoreflect.Message) {
	if dst.Descriptor() == src.Descriptor() {
		proto.Merge(dst.Interface(), src.Interface())
		return
	}
	b, err := proto.MarshalOptions{AllowPartial: true}.Marshal(src.Interface())
	if err == nil {
		err = proto.UnmarshalOptions{AllowPartial: true, Merge: true}.Unmarshal(b, dst.Interface())
	}
	if err != nil {
		panic(fmt.Sprintf("protodiffgen: copying %s: %v", src.Descriptor().FullName(), err))
	}
}
