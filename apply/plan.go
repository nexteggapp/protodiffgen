package apply

import (
	"fmt"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/nexteggapp/protodiffgen/internal/schema"
)

// kind says how a field of a patch body changes its source field.
type kind int

const (
	kScalar  kind = iota // a singular scalar or enum, set
	kAtomic              // a singular message, set whole
	kPatch               // a singular message, patched
	kKeyed               // a keyed list or a map
	kIndexed             // an indexed list
	kReplace             // a list set whole
)

// plan is what the runtime knows about one patch body, read from its
// descriptor and options.
type plan struct {
	body   protoreflect.MessageDescriptor
	source protoreflect.FullName
	fields []*fplan
	byNum  map[protoreflect.FieldNumber]*fplan
	clear  protoreflect.FieldDescriptor
}

// fplan is one source field of a patch body.
type fplan struct {
	kind   kind
	num    protoreflect.FieldNumber
	name   protoreflect.FullName // the source field
	diff   protoreflect.FieldDescriptor
	sub    *plan // the item's or message's patch body; nil when set whole
	wrap   protoreflect.MessageDescriptor
	op     protoreflect.OneofDescriptor
	key    protoreflect.FieldDescriptor // the wrapper's key
	keySrc protoreflect.Name            // the key's name in the item
	index  protoreflect.FieldDescriptor
	add    protoreflect.FieldDescriptor
	remove protoreflect.FieldDescriptor
	patch  protoreflect.FieldDescriptor
	set    protoreflect.FieldDescriptor
	insert protoreflect.FieldDescriptor
	items  protoreflect.FieldDescriptor // Inserted.items, or a Replace wrapper's list
	order  protoreflect.FieldDescriptor // in the parent body; ORDERED keyed lists only
}

var (
	planMu sync.Mutex
	plans  = map[protoreflect.MessageDescriptor]*plan{}
	errs   = map[protoreflect.MessageDescriptor]error{}
)

// planFor returns the plan for a patch body.
func planFor(md protoreflect.MessageDescriptor) (*plan, error) {
	planMu.Lock()
	defer planMu.Unlock()
	if p, ok := plans[md]; ok {
		return p, nil
	}
	if err, ok := errs[md]; ok {
		return nil, err
	}
	building := map[protoreflect.MessageDescriptor]*plan{}
	p, err := build(md, building)
	if err != nil {
		errs[md] = err
		return nil, err
	}
	for d, bp := range building {
		plans[d] = bp
	}
	return p, nil
}

func build(md protoreflect.MessageDescriptor, building map[protoreflect.MessageDescriptor]*plan) (*plan, error) {
	if p, ok := plans[md]; ok {
		return p, nil
	}
	if p, ok := building[md]; ok {
		return p, nil
	}
	source, k, ok := schema.MessageChange(md)
	if !ok || k != schema.KindPatch {
		return nil, fmt.Errorf("%s is not a protodiffgen patch body", md.FullName())
	}
	p := &plan{body: md, source: source, byNum: map[protoreflect.FieldNumber]*fplan{}}
	building[md] = p
	orders := map[protoreflect.FullName]protoreflect.FieldDescriptor{}
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Number() == schema.ClearNumber {
			if fd.Kind() != protoreflect.Int32Kind || !fd.IsList() {
				return nil, fmt.Errorf("%s: the clear field is not repeated int32", fd.FullName())
			}
			p.clear = fd
			continue
		}
		src, ok := schema.FieldSourceOf(fd)
		if !ok {
			return nil, fmt.Errorf("%s has no protodiffgen field option", fd.FullName())
		}
		if src.Order() {
			orders[src.Source()] = fd
			continue
		}
		fp := &fplan{num: fd.Number(), name: src.Source(), diff: fd}
		if err := fp.classify(building); err != nil {
			return nil, err
		}
		p.fields = append(p.fields, fp)
		p.byNum[fp.num] = fp
	}
	if p.clear == nil {
		return nil, fmt.Errorf("%s has no clear field", md.FullName())
	}
	for name, fd := range orders {
		var found bool
		for _, fp := range p.fields {
			if fp.name == name && fp.kind == kKeyed {
				fp.order = fd
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%s: order field for unknown list %s", fd.FullName(), name)
		}
	}
	return p, nil
}

func (fp *fplan) classify(building map[protoreflect.MessageDescriptor]*plan) error {
	fd := fp.diff
	if fd.Message() == nil {
		if fd.IsList() {
			return fmt.Errorf("%s: unexpected repeated scalar", fd.FullName())
		}
		fp.kind = kScalar
		return nil
	}
	wm := fd.Message()
	_, k, ok := schema.MessageChange(wm)
	if !ok {
		k = schema.KindUnspecified
	}
	switch {
	case fd.IsList() && k == schema.KindListChange:
		fp.kind = kKeyed
	case fd.IsList() && k == schema.KindListEdit:
		fp.kind = kIndexed
	case !fd.IsList() && k == schema.KindReplace:
		fp.kind = kReplace
	case !fd.IsList() && k == schema.KindPatch:
		fp.kind = kPatch
		sub, err := build(wm, building)
		if err != nil {
			return err
		}
		fp.sub = sub
		return nil
	case !fd.IsList() && k == schema.KindUnspecified:
		fp.kind = kAtomic
		return nil
	default:
		return fmt.Errorf("%s: unexpected type %s", fd.FullName(), wm.FullName())
	}
	fp.wrap = wm
	wf := wm.Fields()
	byName := func(name protoreflect.Name) protoreflect.FieldDescriptor { return wf.ByName(name) }
	if fp.kind == kReplace {
		fp.items = wf.ByNumber(1)
		if fp.items == nil || !fp.items.IsList() {
			return fmt.Errorf("%s has no list field", wm.FullName())
		}
		return nil
	}
	fp.op = wm.Oneofs().ByName("op")
	if fp.op == nil {
		return fmt.Errorf("%s has no op oneof", wm.FullName())
	}
	fp.remove = byName("remove")
	fp.patch = byName("patch")
	for i := 0; i < wf.Len(); i++ {
		if s, ok := schema.FieldSourceOf(wf.Get(i)); ok {
			if s.Key() {
				fp.key = wf.Get(i)
				fp.keySrc = s.Source().Name()
			}
			if s.Index() {
				fp.index = wf.Get(i)
			}
		}
	}
	if fp.kind == kKeyed {
		fp.add = byName("add")
		fp.set = byName("set")
		if fp.key == nil || fp.remove == nil || (fp.set == nil && (fp.add == nil || fp.patch == nil)) {
			return fmt.Errorf("%s is not a change wrapper", wm.FullName())
		}
	} else {
		fp.insert = byName("insert_before")
		if fp.index == nil || fp.remove == nil || fp.patch == nil || fp.insert == nil || fp.insert.Message() == nil {
			return fmt.Errorf("%s is not an indexed edit", wm.FullName())
		}
		fp.items = fp.insert.Message().Fields().ByName("items")
		if fp.items == nil {
			return fmt.Errorf("%s has no items", fp.insert.Message().FullName())
		}
	}
	if fp.patch != nil && fp.patch.Message() != nil {
		if _, k, ok := schema.MessageChange(fp.patch.Message()); ok && k == schema.KindPatch {
			sub, err := build(fp.patch.Message(), building)
			if err != nil {
				return err
			}
			fp.sub = sub
		}
	}
	return nil
}

// present reports whether d changes the field: sets it, or for an ORDERED
// list, changes its items or its order.
func (fp *fplan) present(d protoreflect.Message) bool {
	return d.Has(fp.diff) || fp.order != nil && d.Has(fp.order)
}

// bound caches which plans have been checked against which target types.
var bound sync.Map

type bindKey struct {
	p  *plan
	md protoreflect.MessageDescriptor
}

// bind checks that p is a patch body for messages of type md.
func (p *plan) bind(md protoreflect.MessageDescriptor) error {
	if _, ok := bound.Load(bindKey{p, md}); ok {
		return nil
	}
	if md.FullName() != p.source {
		return fmt.Errorf("%s diffs %s, not %s", p.body.FullName(), p.source, md.FullName())
	}
	for _, fp := range p.fields {
		tfd := md.Fields().ByNumber(fp.num)
		if tfd == nil || tfd.FullName() != fp.name {
			return fmt.Errorf("%s: %s has no field %s numbered %d", p.body.FullName(), md.FullName(), fp.name, fp.num)
		}
		ok := true
		switch fp.kind {
		case kScalar:
			ok = !tfd.IsList() && tfd.Message() == nil && tfd.Kind() == fp.diff.Kind()
		case kAtomic, kPatch:
			ok = !tfd.IsList() && tfd.Message() != nil
		case kKeyed:
			ok = tfd.IsMap() || (tfd.IsList() && tfd.Message() != nil && tfd.Message().Fields().ByName(fp.keySrc) != nil)
		case kIndexed, kReplace:
			ok = tfd.IsList() && !tfd.IsMap()
		}
		if !ok {
			return fmt.Errorf("%s: field %s doesn't match %s", p.body.FullName(), fp.diff.Name(), tfd.FullName())
		}
	}
	bound.Store(bindKey{p, md}, true)
	return nil
}

// rules maps each source type to its patch body's plan, for comparing
// messages under the list rules of one generated file.
type rules map[protoreflect.FullName]*plan

var rulesCache sync.Map

func rulesFor(md protoreflect.MessageDescriptor) rules {
	file := md.ParentFile()
	if r, ok := rulesCache.Load(file); ok {
		return r.(rules)
	}
	r := rules{}
	msgs := file.Messages()
	for i := 0; i < msgs.Len(); i++ {
		if p, err := planFor(msgs.Get(i)); err == nil {
			r[p.source] = p
		}
	}
	rulesCache.Store(file, r)
	return r
}
