package apply

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Item is one place two diffs overlap: an item's path from the root, such as
// members[ada], and the field there, or 0 for the whole item.
type Item struct {
	path  []Step
	field protoreflect.FieldNumber
}

// Path returns the list items from the root to the item. It is empty for the
// root.
func (i Item) Path() []Step { return append([]Step(nil), i.path...) }

// Field returns the number of the item's field both diffs change, or 0 when
// they overlap on the whole item.
func (i Item) Field() protoreflect.FieldNumber { return i.field }

// String returns the item as a path, such as "members[ada] field 2".
func (i Item) String() string {
	parts := make([]string, len(i.path))
	for j, s := range i.path {
		parts[j] = s.String()
	}
	out := strings.Join(parts, ".")
	if i.field != 0 {
		if out != "" {
			out += " "
		}
		out += "field " + strconv.Itoa(int(i.field))
	}
	if out == "" {
		return "root"
	}
	return out
}

// Step is one list item on a path: by key in a keyed list, where an enum key
// is held as its number, or by base index in an indexed one.
type Step struct {
	list  protoreflect.FullName
	key   protoreflect.MapKey
	index int
}

// List returns the full name of the list field, such as
// "example.v1.Team.members".
func (s Step) List() protoreflect.FullName { return s.list }

// Key returns the item's key. It is not valid for a step in an indexed list.
func (s Step) Key() protoreflect.MapKey { return s.key }

// Index returns the item's base index in an indexed list, or -1 for a step in
// a keyed list.
func (s Step) Index() int { return s.index }

var plainKey = regexp.MustCompile(`^[A-Za-z0-9_.\-]*$`)

// String returns the step, such as "members[ada]" or "addresses[2]".
func (s Step) String() string {
	name := string(s.list.Name())
	if s.index >= 0 {
		return fmt.Sprintf("%s[%d]", name, s.index)
	}
	k := fmt.Sprint(s.key.Interface())
	if str, ok := s.key.Interface().(string); ok && (str == "" || !plainKey.MatchString(str)) {
		k = strconv.Quote(str)
	}
	return name + "[" + k + "]"
}

// Overlap lists where two diffs of one base both change something.
//
// An item is the root message or one keyed list item. Two diffs overlap when
// both change the same field of the same item, or when one adds, removes or
// replaces an ancestor of what the other changes. Setting an atomic message,
// a REPLACE list or a new oneof case replaces everything inside it. Changes
// inside a singular message count as changes to the item's field that holds
// it. In an indexed list, two diffs overlap at a base index when both patch or
// remove it, both insert at it, or one patches or removes it and the other
// inserts at it.
func Overlap(d1, d2 proto.Message) ([]Item, error) {
	m1, m2 := d1.ProtoReflect(), d2.ProtoReflect()
	if m1.Descriptor() != m2.Descriptor() {
		return nil, fmt.Errorf("d1 is %s but d2 is %s", m1.Descriptor().FullName(), m2.Descriptor().FullName())
	}
	p, err := planFor(m1.Descriptor())
	if err != nil {
		return nil, err
	}
	o := &overlaps{seen: map[string]bool{}}
	o.message(p, m1, m2, nil, 0)
	return o.items, nil
}

type overlaps struct {
	items []Item
	seen  map[string]bool
}

func (o *overlaps) add(path []Step, field protoreflect.FieldNumber) {
	it := Item{path: append([]Step(nil), path...), field: field}
	if s := fmt.Sprintf("%#v", stepKeys(it)); !o.seen[s] {
		o.seen[s] = true
		o.items = append(o.items, it)
	}
}

func stepKeys(it Item) []any {
	out := []any{it.field}
	for _, s := range it.path {
		var k any
		if s.key.IsValid() {
			k = s.key.Interface()
		}
		out = append(out, string(s.list), k, s.index)
	}
	return out
}

// message compares two patches of one item. top is the item's field that
// holds them, or 0 when they patch the item itself.
func (o *overlaps) message(p *plan, x, y protoreflect.Message, path []Step, top protoreflect.FieldNumber) {
	cx, cy := clears(p, x), clears(p, y)
	at := func(fp *fplan) protoreflect.FieldNumber {
		if top != 0 {
			return top
		}
		return fp.num
	}
	touched := func(m protoreflect.Message, c map[protoreflect.FieldNumber]bool, fp *fplan) bool {
		return m.Has(fp.diff) || c[fp.num]
	}
	// Inside a singular message, any two changes outside list items are
	// changes to the one field of the item that holds it.
	if top != 0 && direct(p, x) && direct(p, y) {
		o.add(path, top)
	}
	for _, fp := range p.fields {
		hx, hy := x.Has(fp.diff), y.Has(fp.diff)
		switch fp.kind {
		case kScalar, kAtomic, kReplace:
			if touched(x, cx, fp) && touched(y, cy, fp) {
				o.add(path, at(fp))
			}
		case kPatch:
			switch {
			case cx[fp.num] && touched(y, cy, fp), cy[fp.num] && hx:
				o.add(path, at(fp))
			case hx && hy:
				o.message(fp.sub, x.Get(fp.diff).Message(), y.Get(fp.diff).Message(), path, at(fp))
			}
		case kKeyed:
			o.keyed(fp, x.Get(fp.diff).List(), y.Get(fp.diff).List(), path)
			if fp.order != nil && x.Has(fp.order) && y.Has(fp.order) {
				o.add(path, at(fp))
			}
		case kIndexed:
			o.indexed(fp, x.Get(fp.diff).List(), y.Get(fp.diff).List(), path)
		}
	}
	// Two diffs that change different cases of one oneof overlap.
	oneofs := p.body.Oneofs()
	for i := 0; i < oneofs.Len(); i++ {
		var fx, fy *fplan
		for _, fp := range p.fields {
			if fp.diff.ContainingOneof() != oneofs.Get(i) {
				continue
			}
			if touched(x, cx, fp) && fx == nil {
				fx = fp
			}
			if touched(y, cy, fp) && fy == nil {
				fy = fp
			}
		}
		if fx != nil && fy != nil && fx != fy {
			o.add(path, at(fx))
		}
	}
}

// direct reports whether m changes anything outside list items.
func direct(p *plan, m protoreflect.Message) bool {
	if m.Get(p.clear).List().Len() > 0 {
		return true
	}
	for _, fp := range p.fields {
		switch {
		case fp.order != nil && m.Has(fp.order):
			return true
		case !m.Has(fp.diff):
		case fp.kind == kScalar, fp.kind == kAtomic, fp.kind == kReplace:
			return true
		case fp.kind == kPatch && direct(fp.sub, m.Get(fp.diff).Message()):
			return true
		}
	}
	return false
}

func clears(p *plan, m protoreflect.Message) map[protoreflect.FieldNumber]bool {
	out := map[protoreflect.FieldNumber]bool{}
	l := m.Get(p.clear).List()
	for i := 0; i < l.Len(); i++ {
		out[protoreflect.FieldNumber(l.Get(i).Int())] = true
	}
	return out
}

func (o *overlaps) keyed(fp *fplan, x, y protoreflect.List, path []Step) {
	byKey := func(l protoreflect.List) map[any]protoreflect.Message {
		m := map[any]protoreflect.Message{}
		for i := 0; i < l.Len(); i++ {
			w := l.Get(i).Message()
			m[keyOf(fp.key, w.Get(fp.key)).Interface()] = w
		}
		return m
	}
	ym := byKey(y)
	for i := 0; i < x.Len(); i++ {
		wx := x.Get(i).Message()
		k := keyOf(fp.key, wx.Get(fp.key))
		wy, ok := ym[k.Interface()]
		if !ok {
			continue
		}
		step := append(append([]Step(nil), path...), Step{list: fp.name, key: k, index: -1})
		ox, oy := wx.WhichOneof(fp.op), wy.WhichOneof(fp.op)
		if fp.sub != nil && ox == fp.patch && oy == fp.patch {
			o.message(fp.sub, wx.Get(ox).Message(), wy.Get(oy).Message(), step, 0)
			continue
		}
		o.add(step, 0)
	}
}

func (o *overlaps) indexed(fp *fplan, x, y protoreflect.List, path []Step) {
	type marks struct{ insert, change bool }
	read := func(l protoreflect.List) map[uint64]*marks {
		m := map[uint64]*marks{}
		for i := 0; i < l.Len(); i++ {
			e := l.Get(i).Message()
			idx := e.Get(fp.index).Uint()
			if m[idx] == nil {
				m[idx] = &marks{}
			}
			if e.WhichOneof(fp.op) == fp.insert {
				m[idx].insert = true
			} else {
				m[idx].change = true
			}
		}
		return m
	}
	mx, my := read(x), read(y)
	for i := 0; i < x.Len(); i++ {
		idx := x.Get(i).Message().Get(fp.index).Uint()
		a, b := mx[idx], my[idx]
		if b == nil {
			continue
		}
		if (a.change && b.change) || (a.insert && b.insert) || (a.change && b.insert) || (a.insert && b.change) {
			o.add(append(append([]Step(nil), path...), Step{list: fp.name, index: int(idx)}), 0)
		}
	}
}
