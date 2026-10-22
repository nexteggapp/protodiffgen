package apply

import (
	"math"
	"math/rand/v2"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// rgen makes random messages, mutations and diffs of a test schema. Values
// come from small sets, so that two random messages often share keys and
// values.
type rgen struct {
	r     *rand.Rand
	rules rules
}

func (g *rgen) pick(n int) int { return g.r.IntN(n) }

func (g *rgen) chance(p float64) bool { return g.r.Float64() < p }

func (g *rgen) scalar(fd protoreflect.FieldDescriptor) protoreflect.Value {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(g.chance(0.5))
	case protoreflect.EnumKind:
		vals := fd.Enum().Values()
		if !fd.Enum().IsClosed() && g.chance(0.15) {
			return protoreflect.ValueOfEnum(9)
		}
		return protoreflect.ValueOfEnum(vals.Get(g.pick(vals.Len())).Number())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32([]int32{-1, 0, 1, 2, 7}[g.pick(5)])
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64([]int64{-1, 0, 1, 2, 7}[g.pick(5)])
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(uint32(g.pick(4)))
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(uint64(g.pick(4)))
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(float32(floats[g.pick(len(floats))]))
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(floats[g.pick(len(floats))])
	case protoreflect.StringKind:
		return protoreflect.ValueOfString([]string{"", "a", "b", "ada", "é", "x y"}[g.pick(6)])
	case protoreflect.BytesKind:
		return protoreflect.ValueOfBytes([][]byte{nil, []byte("a"), {0, 0xff}}[g.pick(3)])
	}
	panic(fd.Kind())
}

var floats = []float64{0, math.Copysign(0, -1), 1.5, math.NaN(), math.Inf(1), -2}

// key returns a random key value from a small domain.
func (g *rgen) key(fd protoreflect.FieldDescriptor) protoreflect.Value {
	n := g.pick(6)
	switch fd.Kind() {
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(string(rune('a' + n)))
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(n%2 == 1)
	case protoreflect.EnumKind:
		vals := fd.Enum().Values()
		return protoreflect.ValueOfEnum(vals.Get(n % vals.Len()).Number())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(int32(n + 1))
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(int64(n + 1))
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(uint32(n + 1))
	}
	return protoreflect.ValueOfUint64(uint64(n + 1))
}

func (g *rgen) keyedRule(md protoreflect.MessageDescriptor, fd protoreflect.FieldDescriptor) *fplan {
	if p := g.rules[md.FullName()]; p != nil {
		if fp := p.byNum[fd.Number()]; fp != nil && fp.kind == kKeyed && !fd.IsMap() {
			return fp
		}
	}
	return nil
}

// message returns a random message of type md.
func (g *rgen) message(md protoreflect.MessageDescriptor, depth int) protoreflect.Message {
	m := dynamicpb.NewMessage(md)
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if g.chance(0.45) {
			continue
		}
		g.setRandom(m, fd, depth)
	}
	return m
}

func (g *rgen) setRandom(m protoreflect.Message, fd protoreflect.FieldDescriptor, depth int) {
	md := m.Descriptor()
	if depth <= 0 && (fd.Message() != nil && !fd.IsMap() || fd.IsMap() && fd.MapValue().Message() != nil) {
		return
	}
	switch {
	case fd.IsMap():
		mp := m.Mutable(fd).Map()
		for n := g.pick(4); n > 0; n-- {
			mp.Set(g.key(fd.MapKey()).MapKey(), g.element(fd.MapValue(), depth))
		}
	case fd.IsList():
		l := m.Mutable(fd).List()
		n := g.pick(5)
		if fp := g.keyedRule(md, fd); fp != nil {
			keyFd := fd.Message().Fields().ByName(fp.keySrc)
			seen := map[any]bool{}
			for t := 0; t < 3*n && l.Len() < n; t++ {
				k := g.key(keyFd)
				if seen[keyOf(keyFd, k).Interface()] {
					continue
				}
				seen[keyOf(keyFd, k).Interface()] = true
				item := g.message(fd.Message(), depth-1)
				item.Set(keyFd, k)
				l.Append(protoreflect.ValueOfMessage(item))
			}
			return
		}
		for ; n > 0; n-- {
			l.Append(g.element(fd, depth))
		}
	default:
		m.Set(fd, g.element(fd, depth))
	}
}

func (g *rgen) element(fd protoreflect.FieldDescriptor, depth int) protoreflect.Value {
	if fd.Message() != nil {
		return protoreflect.ValueOfMessage(g.message(fd.Message(), depth-1))
	}
	return g.scalar(fd)
}

// mutate changes m in place, keeping keyed lists valid.
func (g *rgen) mutate(m protoreflect.Message, depth int) {
	md := m.Descriptor()
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if g.chance(0.65) {
			continue
		}
		switch {
		case fd.IsMap():
			mp := m.Mutable(fd).Map()
			for n := g.pick(3); n > 0; n-- {
				k := g.key(fd.MapKey()).MapKey()
				switch {
				case g.chance(0.3):
					mp.Clear(k)
				case mp.Has(k) && fd.MapValue().Message() != nil && g.chance(0.6):
					g.mutate(mp.Mutable(k).Message(), depth-1)
				default:
					mp.Set(k, g.element(fd.MapValue(), depth))
				}
			}
		case fd.IsList():
			if fp := g.keyedRule(md, fd); fp != nil {
				g.mutateKeyed(m, fd, fp, depth)
			} else {
				g.mutateList(m.Mutable(fd).List(), fd, depth)
			}
		case fd.Message() != nil:
			switch {
			case g.chance(0.2):
				m.Clear(fd)
			case m.Has(fd) && g.chance(0.6):
				g.mutate(m.Mutable(fd).Message(), depth-1)
			case depth > 0:
				m.Set(fd, g.element(fd, depth))
			}
		default:
			if fd.HasPresence() && g.chance(0.2) {
				m.Clear(fd)
			} else {
				m.Set(fd, g.scalar(fd))
			}
		}
	}
}

func (g *rgen) mutateKeyed(m protoreflect.Message, fd protoreflect.FieldDescriptor, fp *fplan, depth int) {
	l := m.Mutable(fd).List()
	keyFd := fd.Message().Fields().ByName(fp.keySrc)
	var items []protoreflect.Value
	for i := 0; i < l.Len(); i++ {
		items = append(items, l.Get(i))
	}
	for n := g.pick(4); n > 0; n-- {
		switch {
		case len(items) > 0 && g.chance(0.3):
			j := g.pick(len(items))
			items = append(items[:j], items[j+1:]...)
		case len(items) > 0 && g.chance(0.5):
			item := items[g.pick(len(items))].Message()
			k := item.Get(keyFd)
			g.mutate(item, depth-1)
			item.Set(keyFd, k)
		default:
			k := g.key(keyFd)
			dup := false
			for _, it := range items {
				if keyOf(keyFd, it.Message().Get(keyFd)).Interface() == keyOf(keyFd, k).Interface() {
					dup = true
				}
			}
			if !dup {
				item := g.message(fd.Message(), depth-1)
				item.Set(keyFd, k)
				items = append(items, protoreflect.ValueOfMessage(item))
			}
		}
	}
	if g.chance(0.3) {
		g.r.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
	}
	l.Truncate(0)
	for _, it := range items {
		l.Append(it)
	}
}

func (g *rgen) mutateList(l protoreflect.List, fd protoreflect.FieldDescriptor, depth int) {
	var items []protoreflect.Value
	for i := 0; i < l.Len(); i++ {
		items = append(items, l.Get(i))
	}
	for n := g.pick(4); n > 0; n-- {
		switch {
		case len(items) > 0 && g.chance(0.3):
			j := g.pick(len(items))
			items = append(items[:j], items[j+1:]...)
		case len(items) > 0 && g.chance(0.4):
			j := g.pick(len(items))
			if fd.Message() != nil {
				g.mutate(items[j].Message(), depth-1)
			} else {
				items[j] = g.scalar(fd)
			}
		default:
			j := g.pick(len(items) + 1)
			v := g.element(fd, depth)
			items = append(items[:j], append([]protoreflect.Value{v}, items[j:]...)...)
		}
	}
	l.Truncate(0)
	for _, it := range items {
		l.Append(it)
	}
}

// diff returns a random diff that Validate accepts against a, often with
// entries that change nothing.
func (g *rgen) diff(p *plan, a protoreflect.Message, depth int) protoreflect.Message {
	d := dynamicpb.NewMessage(p.body)
	fields := a.Descriptor().Fields()
	var clears []int32
	setCase := map[protoreflect.Name]bool{}
	for _, fp := range p.fields {
		if g.chance(0.6) {
			continue
		}
		tfd := fields.ByNumber(fp.num)
		oneof := tfd.ContainingOneof()
		if oneof != nil && oneof.IsSynthetic() {
			oneof = nil
		}
		if oneof != nil && setCase[oneof.Name()] {
			continue
		}
		if tfd.HasPresence() && g.chance(0.25) {
			clears = append(clears, int32(fp.num))
			continue
		}
		active := oneof == nil || a.WhichOneof(oneof) == tfd
		switch fp.kind {
		case kScalar:
			if active && a.Has(tfd) && g.chance(0.3) {
				d.Set(fp.diff, a.Get(tfd))
			} else {
				d.Set(fp.diff, g.scalar(tfd))
			}
		case kAtomic:
			if active && a.Has(tfd) && g.chance(0.3) {
				d.Set(fp.diff, protoreflect.ValueOfMessage(proto.Clone(a.Get(tfd).Message().Interface()).ProtoReflect()))
			} else if depth > 0 {
				d.Set(fp.diff, protoreflect.ValueOfMessage(g.message(tfd.Message(), depth-1)))
			} else {
				continue
			}
		case kPatch:
			if depth <= 0 {
				continue
			}
			base := a.Get(tfd).Message()
			if !active || !a.Has(tfd) {
				base = dynamicpb.NewMessage(tfd.Message())
			}
			d.Set(fp.diff, protoreflect.ValueOfMessage(g.diff(fp.sub, base, depth-1)))
		case kKeyed:
			if tfd.IsMap() {
				g.mapDiff(fp, tfd, a.Get(tfd).Map(), d, depth)
			} else {
				g.keyedDiff(fp, tfd, a.Get(tfd).List(), d, depth)
			}
		case kIndexed:
			g.indexedDiff(fp, tfd, a.Get(tfd).List(), d, depth)
		case kReplace:
			w := d.Mutable(fp.diff).Message()
			l := w.Mutable(fp.items).List()
			if g.chance(0.3) {
				src := a.Get(tfd).List()
				for i := 0; i < src.Len(); i++ {
					l.Append(src.Get(i))
				}
			} else {
				for n := g.pick(4); n > 0; n-- {
					l.Append(g.element(tfd, depth))
				}
			}
		}
		if oneof != nil && d.Has(fp.diff) {
			setCase[oneof.Name()] = true
		}
	}
	// A clear of a field that is also set is invalid; drop those.
	l := d.Mutable(p.clear).List()
	for _, n := range clears {
		if fp := p.byNum[protoreflect.FieldNumber(n)]; fp != nil && d.Has(fp.diff) {
			continue
		}
		l.Append(protoreflect.ValueOfInt32(n))
	}
	if l.Len() == 0 {
		d.Clear(p.clear)
	}
	return d
}

func (g *rgen) keyedDiff(fp *fplan, tfd protoreflect.FieldDescriptor, a protoreflect.List, d protoreflect.Message, depth int) {
	keyFd := tfd.Message().Fields().ByName(fp.keySrc)
	have, _ := indexKeys(a, keyFd)
	var result []protoreflect.Value
	l := d.Mutable(fp.diff).List()
	var entries []protoreflect.Value
	for i := 0; i < a.Len(); i++ {
		item := a.Get(i).Message()
		k := item.Get(keyFd)
		switch g.pick(4) {
		case 0:
			w := l.NewElement().Message()
			w.Set(fp.key, k)
			w.Set(fp.remove, w.NewField(fp.remove))
			entries = append(entries, protoreflect.ValueOfMessage(w))
			continue
		case 1:
			if depth <= 0 {
				break
			}
			w := l.NewElement().Message()
			w.Set(fp.key, k)
			if fp.sub == nil {
				np := proto.Clone(item.Interface()).ProtoReflect()
				if g.chance(0.6) {
					g.mutate(np, depth-1)
					np.Set(keyFd, k)
				}
				w.Set(fp.patch, protoreflect.ValueOfMessage(np))
			} else {
				patch := g.diff(fp.sub, item, depth-1)
				g.keepKey(fp.sub, keyFd, k, patch)
				w.Set(fp.patch, protoreflect.ValueOfMessage(patch))
			}
			entries = append(entries, protoreflect.ValueOfMessage(w))
		}
		result = append(result, k)
	}
	for n := g.pick(3); n > 0; n-- {
		k := g.key(keyFd)
		if _, ok := have[keyOf(keyFd, k).Interface()]; ok {
			continue
		}
		have[keyOf(keyFd, k).Interface()] = -1
		item := g.message(tfd.Message(), depth-1)
		item.Set(keyFd, k)
		w := l.NewElement().Message()
		w.Set(fp.key, k)
		w.Set(fp.add, protoreflect.ValueOfMessage(item))
		entries = append(entries, protoreflect.ValueOfMessage(w))
		result = append(result, k)
	}
	g.r.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
	for _, e := range entries {
		l.Append(e)
	}
	if l.Len() == 0 {
		d.Clear(fp.diff)
	}
	if fp.order != nil && g.chance(0.4) {
		g.r.Shuffle(len(result), func(i, j int) { result[i], result[j] = result[j], result[i] })
		ol := d.Mutable(fp.order).List()
		for _, k := range result {
			ol.Append(k)
		}
		if ol.Len() == 0 {
			d.Clear(fp.order)
		}
	}
}

// keepKey removes from a keyed item's patch anything that changes its key.
func (g *rgen) keepKey(sub *plan, keyFd protoreflect.FieldDescriptor, k protoreflect.Value, patch protoreflect.Message) {
	if fp := sub.byNum[keyFd.Number()]; fp != nil && patch.Has(fp.diff) {
		if keyOf(keyFd, patch.Get(fp.diff)).Interface() != keyOf(keyFd, k).Interface() {
			patch.Clear(fp.diff)
		}
	}
	cl := patch.Get(sub.clear).List()
	var keep []protoreflect.Value
	for i := 0; i < cl.Len(); i++ {
		if protoreflect.FieldNumber(cl.Get(i).Int()) != keyFd.Number() {
			keep = append(keep, cl.Get(i))
		}
	}
	patch.Clear(sub.clear)
	if len(keep) > 0 {
		nl := patch.Mutable(sub.clear).List()
		for _, v := range keep {
			nl.Append(v)
		}
	}
}

func (g *rgen) mapDiff(fp *fplan, tfd protoreflect.FieldDescriptor, a protoreflect.Map, d protoreflect.Message, depth int) {
	l := d.Mutable(fp.diff).List()
	var entries []protoreflect.Value
	seen := map[any]bool{}
	a.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		seen[k.Interface()] = true
		w := l.NewElement().Message()
		w.Set(fp.key, k.Value())
		switch g.pick(4) {
		case 0:
			w.Set(fp.remove, w.NewField(fp.remove))
		case 1:
			switch {
			case fp.set != nil && g.chance(0.3):
				w.Set(fp.set, v)
			case fp.set != nil:
				w.Set(fp.set, g.element(tfd.MapValue(), depth))
			case depth > 0:
				w.Set(fp.patch, protoreflect.ValueOfMessage(g.diff(fp.sub, v.Message(), depth-1)))
			default:
				return true
			}
		default:
			return true
		}
		entries = append(entries, protoreflect.ValueOfMessage(w))
		return true
	})
	for n := g.pick(3); n > 0; n-- {
		k := g.key(tfd.MapKey()).MapKey()
		if seen[k.Interface()] {
			continue
		}
		seen[k.Interface()] = true
		w := l.NewElement().Message()
		w.Set(fp.key, k.Value())
		if fp.set != nil {
			w.Set(fp.set, g.element(tfd.MapValue(), depth))
		} else {
			w.Set(fp.add, g.element(tfd.MapValue(), depth))
		}
		entries = append(entries, protoreflect.ValueOfMessage(w))
	}
	g.r.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
	for _, e := range entries {
		l.Append(e)
	}
	if l.Len() == 0 {
		d.Clear(fp.diff)
	}
}

func (g *rgen) indexedDiff(fp *fplan, tfd protoreflect.FieldDescriptor, a protoreflect.List, d protoreflect.Message, depth int) {
	n := a.Len()
	l := d.Mutable(fp.diff).List()
	var entries []protoreflect.Value
	edit := func(idx int) protoreflect.Message {
		e := l.NewElement().Message()
		e.Set(fp.index, protoreflect.ValueOfUint32(uint32(idx)))
		entries = append(entries, protoreflect.ValueOfMessage(e))
		return e
	}
	for i := 0; i < n; i++ {
		switch g.pick(5) {
		case 0:
			e := edit(i)
			e.Set(fp.remove, e.NewField(fp.remove))
		case 1:
			e := edit(i)
			switch {
			case fp.sub != nil:
				e.Set(fp.patch, protoreflect.ValueOfMessage(g.diff(fp.sub, a.Get(i).Message(), depth-1)))
			case g.chance(0.3):
				e.Set(fp.patch, a.Get(i))
			default:
				e.Set(fp.patch, g.element(tfd, depth))
			}
		}
	}
	for i := 0; i <= n; i++ {
		if g.chance(0.2) {
			e := edit(i)
			items := e.Mutable(fp.insert).Message().Mutable(fp.items).List()
			for c := 1 + g.pick(2); c > 0; c-- {
				items.Append(g.element(tfd, depth))
			}
		}
	}
	g.r.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
	for _, e := range entries {
		l.Append(e)
	}
	if l.Len() == 0 {
		d.Clear(fp.diff)
	}
}
