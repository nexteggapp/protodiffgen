package apply

import (
	"sort"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Apply validates d against a, then returns a with d applied. a is not changed.
func Apply(a, d proto.Message) (proto.Message, error) {
	if err := Validate(a, d); err != nil {
		return nil, err
	}
	out := proto.Clone(a)
	p, _ := planFor(d.ProtoReflect().Descriptor())
	apply(p, out.ProtoReflect(), d.ProtoReflect())
	return out, nil
}

// apply changes m by d, which Validate has accepted.
func apply(p *plan, m, d protoreflect.Message) {
	fields := m.Descriptor().Fields()
	cl := d.Get(p.clear).List()
	for i := 0; i < cl.Len(); i++ {
		m.Clear(fields.ByNumber(protoreflect.FieldNumber(cl.Get(i).Int())))
	}
	for _, fp := range p.fields {
		if !fp.present(d) {
			continue
		}
		tfd := fields.ByNumber(fp.num)
		v := d.Get(fp.diff)
		switch fp.kind {
		case kScalar:
			m.Set(tfd, v)
		case kAtomic:
			m.Set(tfd, convert(m, tfd, v))
		case kPatch:
			// Mutable starts an unset message, or a new oneof case, from empty.
			apply(fp.sub, m.Mutable(tfd).Message(), v.Message())
		case kKeyed:
			if tfd.IsMap() {
				applyMap(fp, tfd, m.Mutable(tfd).Map(), v.List())
			} else {
				applyKeyed(fp, tfd, m, d)
			}
		case kIndexed:
			applyIndexed(fp, m.Mutable(tfd).List(), v.List())
		case kReplace:
			m.Clear(tfd)
			src := v.Message().Get(fp.items).List()
			if src.Len() > 0 {
				dst := m.Mutable(tfd).List()
				for i := 0; i < src.Len(); i++ {
					dst.Append(convertElem(dst, src.Get(i)))
				}
			}
		}
	}
}

func applyKeyed(fp *fplan, tfd protoreflect.FieldDescriptor, m, d protoreflect.Message) {
	l := m.Mutable(tfd).List()
	keyFd := tfd.Message().Fields().ByName(fp.keySrc)
	pos, _ := indexKeys(l, keyFd)
	items := make([]protoreflect.Value, l.Len())
	for i := range items {
		items[i] = l.Get(i)
	}
	removed := map[int]bool{}
	type added struct {
		key protoreflect.MapKey
		val protoreflect.Value
	}
	var adds []added
	changes := d.Get(fp.diff).List()
	for i := 0; i < changes.Len(); i++ {
		w := changes.Get(i).Message()
		k := keyOf(fp.key, w.Get(fp.key))
		switch op := w.WhichOneof(fp.op); op {
		case fp.add:
			adds = append(adds, added{k, convertElem(l, w.Get(op))})
		case fp.remove:
			removed[pos[k.Interface()]] = true
		case fp.patch:
			j := pos[k.Interface()]
			if fp.sub == nil {
				items[j] = convertElem(l, w.Get(op))
			} else {
				apply(fp.sub, items[j].Message(), w.Get(op).Message())
			}
		}
	}
	sort.Slice(adds, func(i, j int) bool { return lessKey(adds[i].key, adds[j].key) })
	var result []protoreflect.Value
	for i, v := range items {
		if !removed[i] {
			result = append(result, v)
		}
	}
	for _, a := range adds {
		result = append(result, a.val)
	}
	if fp.order != nil && d.Has(fp.order) {
		rank := map[any]int{}
		order := d.Get(fp.order).List()
		for i := 0; i < order.Len(); i++ {
			rank[keyOf(fp.order, order.Get(i)).Interface()] = i
		}
		sort.SliceStable(result, func(i, j int) bool {
			ki := keyOf(keyFd, result[i].Message().Get(keyFd)).Interface()
			kj := keyOf(keyFd, result[j].Message().Get(keyFd)).Interface()
			return rank[ki] < rank[kj]
		})
	}
	l.Truncate(0)
	for _, v := range result {
		l.Append(v)
	}
}

func applyMap(fp *fplan, tfd protoreflect.FieldDescriptor, mp protoreflect.Map, changes protoreflect.List) {
	for i := 0; i < changes.Len(); i++ {
		w := changes.Get(i).Message()
		k := w.Get(fp.key).MapKey()
		switch op := w.WhichOneof(fp.op); op {
		case fp.remove:
			mp.Clear(k)
		case fp.set, fp.add:
			v := w.Get(op)
			if tfd.MapValue().Message() != nil {
				nv := mp.NewValue()
				copyMessage(nv.Message(), v.Message())
				v = nv
			}
			mp.Set(k, v)
		case fp.patch:
			apply(fp.sub, mp.Mutable(k).Message(), w.Get(op).Message())
		}
	}
}

// applyIndexed builds the list by walking base indexes 0 to n-1: at each it
// emits the insert there, then the element, patched or left out if removed.
// Then it emits the insert at n.
func applyIndexed(fp *fplan, l protoreflect.List, edits protoreflect.List) {
	n := l.Len()
	base := make([]protoreflect.Value, n)
	for i := range base {
		base[i] = l.Get(i)
	}
	inserts := map[int][]protoreflect.Value{}
	removed := map[int]bool{}
	for i := 0; i < edits.Len(); i++ {
		e := edits.Get(i).Message()
		idx := int(e.Get(fp.index).Uint())
		switch op := e.WhichOneof(fp.op); op {
		case fp.insert:
			items := e.Get(op).Message().Get(fp.items).List()
			for j := 0; j < items.Len(); j++ {
				inserts[idx] = append(inserts[idx], convertElem(l, items.Get(j)))
			}
		case fp.remove:
			removed[idx] = true
		case fp.patch:
			if fp.sub != nil {
				apply(fp.sub, base[idx].Message(), e.Get(op).Message())
			} else {
				base[idx] = convertElem(l, e.Get(op))
			}
		}
	}
	var result []protoreflect.Value
	for i := 0; i < n; i++ {
		result = append(result, inserts[i]...)
		if !removed[i] {
			result = append(result, base[i])
		}
	}
	result = append(result, inserts[n]...)
	l.Truncate(0)
	for _, v := range result {
		l.Append(v)
	}
}
