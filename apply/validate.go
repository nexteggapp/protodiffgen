package apply

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Validate reports whether d can be applied to a.
//
// It refuses a diff that adds a key the list has, or removes or patches one
// it lacks; has two entries for one key, or a change wrapper with no op; has a
// wrapper key that differs from its add's key, or a patch that changes a keyed
// item's key; clears a field without explicit presence, or both sets and
// clears one field; has an order field that doesn't name exactly the resulting
// keys, once each; in an indexed list, has a patch or remove index outside
// [0, n) or an insert index outside [0, n], a second insert at one index, two
// of patch and remove at one index, or an insert with no elements; leaves an
// optional key unset in an add; or has unknown fields, as a may not either.
func Validate(a, d proto.Message) error {
	am, dm := a.ProtoReflect(), d.ProtoReflect()
	p, err := planFor(dm.Descriptor())
	if err != nil {
		return err
	}
	if err := p.bind(am.Descriptor()); err != nil {
		return err
	}
	if err := checkData(p, am, "a"); err != nil {
		return err
	}
	return validateDiff(p, am, dm)
}

// validateDiff is Validate after a has been checked: it checks d alone,
// then d against a.
func validateDiff(p *plan, a, d protoreflect.Message) error {
	if path, ok := findUnknown(d, "d"); ok {
		return fmt.Errorf("%s has unknown fields", path)
	}
	return validate(p, a, d, "d")
}

func validate(p *plan, a, d protoreflect.Message, where string) error {
	if err := p.bind(a.Descriptor()); err != nil {
		return err
	}
	fields := a.Descriptor().Fields()
	set := map[protoreflect.FieldNumber]bool{}
	for _, fp := range p.fields {
		if d.Has(fp.diff) {
			set[fp.num] = true
		}
	}
	cl := d.Get(p.clear).List()
	for i := 0; i < cl.Len(); i++ {
		num := protoreflect.FieldNumber(cl.Get(i).Int())
		tfd := fields.ByNumber(num)
		switch {
		case tfd == nil || p.byNum[num] == nil:
			return fmt.Errorf("%s: clears field %d, which %s doesn't have", where, num, a.Descriptor().FullName())
		case !tfd.HasPresence():
			return fmt.Errorf("%s: clears %s, which has no explicit presence; set it to its zero value instead", where, tfd.Name())
		case set[num]:
			return fmt.Errorf("%s: both sets and clears %s", where, tfd.Name())
		}
	}
	for _, fp := range p.fields {
		if !fp.present(d) {
			continue
		}
		tfd := fields.ByNumber(fp.num)
		at := where + "." + string(tfd.Name())
		var err error
		switch fp.kind {
		case kPatch:
			base := a.Get(tfd).Message()
			if o := tfd.ContainingOneof(); o != nil && a.WhichOneof(o) != tfd {
				base = a.NewField(tfd).Message()
			}
			err = validate(fp.sub, base, d.Get(fp.diff).Message(), at)
		case kKeyed:
			if tfd.IsMap() {
				err = validateMap(fp, a.Get(tfd).Map(), d.Get(fp.diff).List(), at)
			} else {
				err = validateKeyed(fp, tfd, a.Get(tfd).List(), d, at)
			}
		case kIndexed:
			err = validateIndexed(fp, a.Get(tfd).List(), d.Get(fp.diff).List(), at)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func validateKeyed(fp *fplan, tfd protoreflect.FieldDescriptor, a protoreflect.List, d protoreflect.Message, where string) error {
	keyFd := tfd.Message().Fields().ByName(fp.keySrc)
	have, _ := indexKeys(a, keyFd)
	result := map[any]bool{}
	for k := range have {
		result[k] = true
	}
	seen := map[any]bool{}
	l := d.Get(fp.diff).List()
	for i := 0; i < l.Len(); i++ {
		w := l.Get(i).Message()
		k := keyOf(fp.key, w.Get(fp.key))
		at := fmt.Sprintf("%s[%v]", where, k.Interface())
		if seen[k.Interface()] {
			return fmt.Errorf("%s: two changes for one key", at)
		}
		seen[k.Interface()] = true
		pos, exists := have[k.Interface()]
		switch op := w.WhichOneof(fp.op); {
		case op == nil:
			return fmt.Errorf("%s: a change with no op", at)
		case op == fp.add:
			if exists {
				return fmt.Errorf("%s: adds a key the list has", at)
			}
			item := w.Get(op).Message()
			if keyFd.HasPresence() && !item.Has(keyFd) {
				return fmt.Errorf("%s: the add leaves the key %s unset", at, keyFd.Name())
			}
			if got := keyOf(keyFd, item.Get(keyFd)); got.Interface() != k.Interface() {
				return fmt.Errorf("%s: the add's key is %v", at, got.Interface())
			}
			if err := checkData(fp.sub, item, at); err != nil {
				return err
			}
			result[k.Interface()] = true
		case op == fp.remove:
			if !exists {
				return fmt.Errorf("%s: removes a key the list lacks", at)
			}
			delete(result, k.Interface())
		case op == fp.patch:
			if !exists {
				return fmt.Errorf("%s: patches a key the list lacks", at)
			}
			item := a.Get(pos).Message()
			patch := w.Get(op).Message()
			if fp.sub == nil {
				if keyFd.HasPresence() && !patch.Has(keyFd) || keyOf(keyFd, patch.Get(keyFd)).Interface() != k.Interface() {
					return fmt.Errorf("%s: the patch changes the key", at)
				}
				continue
			}
			if err := validate(fp.sub, item, patch, at); err != nil {
				return err
			}
			if err := checkKeyPatch(fp.sub, keyFd, k, patch, at); err != nil {
				return err
			}
		}
	}
	if fp.order == nil || !d.Has(fp.order) {
		return nil
	}
	order := d.Get(fp.order).List()
	named := map[any]bool{}
	for i := 0; i < order.Len(); i++ {
		k := keyOf(fp.order, order.Get(i)).Interface()
		if !result[k] || named[k] {
			return fmt.Errorf("%s: the order doesn't name exactly the resulting keys, once each", where)
		}
		named[k] = true
	}
	if len(named) != len(result) {
		return fmt.Errorf("%s: the order doesn't name exactly the resulting keys, once each", where)
	}
	return nil
}

// checkKeyPatch refuses a patch that sets or clears a keyed item's key to
// anything but the key it has.
func checkKeyPatch(sub *plan, keyFd protoreflect.FieldDescriptor, k protoreflect.MapKey, patch protoreflect.Message, where string) error {
	fp := sub.byNum[keyFd.Number()]
	if fp != nil && patch.Has(fp.diff) && keyOf(keyFd, patch.Get(fp.diff)).Interface() != k.Interface() {
		return fmt.Errorf("%s: the patch changes the key", where)
	}
	cl := patch.Get(sub.clear).List()
	for i := 0; i < cl.Len(); i++ {
		if protoreflect.FieldNumber(cl.Get(i).Int()) == keyFd.Number() {
			return fmt.Errorf("%s: the patch changes the key", where)
		}
	}
	return nil
}

func validateMap(fp *fplan, a protoreflect.Map, l protoreflect.List, where string) error {
	seen := map[any]bool{}
	for i := 0; i < l.Len(); i++ {
		w := l.Get(i).Message()
		k := w.Get(fp.key).MapKey()
		at := fmt.Sprintf("%s[%v]", where, k.Interface())
		if seen[k.Interface()] {
			return fmt.Errorf("%s: two changes for one key", at)
		}
		seen[k.Interface()] = true
		switch op := w.WhichOneof(fp.op); {
		case op == nil:
			return fmt.Errorf("%s: a change with no op", at)
		case op == fp.add:
			if a.Has(k) {
				return fmt.Errorf("%s: adds a key the map has", at)
			}
			if err := checkData(fp.sub, w.Get(op).Message(), at); err != nil {
				return err
			}
		case op == fp.remove:
			if !a.Has(k) {
				return fmt.Errorf("%s: removes a key the map lacks", at)
			}
		case op == fp.patch:
			if !a.Has(k) {
				return fmt.Errorf("%s: patches a key the map lacks", at)
			}
			if err := validate(fp.sub, a.Get(k).Message(), w.Get(op).Message(), at); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateIndexed(fp *fplan, a protoreflect.List, l protoreflect.List, where string) error {
	n := a.Len()
	inserts := map[uint64]bool{}
	changes := map[uint64]bool{}
	for i := 0; i < l.Len(); i++ {
		e := l.Get(i).Message()
		idx := e.Get(fp.index).Uint()
		at := fmt.Sprintf("%s[%d]", where, idx)
		switch op := e.WhichOneof(fp.op); {
		case op == nil:
			return fmt.Errorf("%s: an edit with no op", at)
		case op == fp.insert:
			if idx > uint64(n) {
				return fmt.Errorf("%s: insert index %d is outside [0, %d]", where, idx, n)
			}
			if inserts[idx] {
				return fmt.Errorf("%s: a second insert at one index", at)
			}
			inserts[idx] = true
			items := e.Get(op).Message().Get(fp.items).List()
			if items.Len() == 0 {
				return fmt.Errorf("%s: an insert with no elements", at)
			}
			if fp.sub != nil {
				for j := 0; j < items.Len(); j++ {
					if err := checkData(fp.sub, items.Get(j).Message(), at); err != nil {
						return err
					}
				}
			}
		default:
			if idx >= uint64(n) {
				return fmt.Errorf("%s: %s index %d is outside [0, %d)", where, op.Name(), idx, n)
			}
			if changes[idx] {
				return fmt.Errorf("%s: two of patch and remove at one index", at)
			}
			changes[idx] = true
			if op == fp.patch && fp.sub != nil {
				if err := validate(fp.sub, a.Get(int(idx)).Message(), e.Get(op).Message(), at); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// checkData refuses a target message, or an item added by a diff, with
// unknown fields, or with a keyed list whose keys are unset or repeated. p
// may be nil for a message that is set whole.
func checkData(p *plan, m protoreflect.Message, where string) error {
	if path, ok := findUnknown(m, where); ok {
		return fmt.Errorf("%s has unknown fields", path)
	}
	if e := checkKeys(p, m); e != nil {
		return e.at(where)
	}
	return nil
}

// pathError is a check's failure below the message it was given. The path
// to the failure is built only once there is one, as the check returns
// through each step to it, so a message that passes costs no strings.
type pathError struct {
	steps []string // innermost first
	msg   string
}

// under adds the step the failure is under.
func (e *pathError) under(step string) *pathError {
	e.steps = append(e.steps, step)
	return e
}

// at is the failure as an error, its path starting from where.
func (e *pathError) at(where string) error {
	var b strings.Builder
	b.WriteString(where)
	for i := len(e.steps) - 1; i >= 0; i-- {
		b.WriteString(e.steps[i])
	}
	return fmt.Errorf("%s: %s", b.String(), e.msg)
}

func checkKeys(p *plan, m protoreflect.Message) *pathError {
	if p == nil {
		return nil
	}
	fields := m.Descriptor().Fields()
	for _, fp := range p.fields {
		tfd := fields.ByNumber(fp.num)
		if tfd == nil || !m.Has(tfd) {
			continue
		}
		var e *pathError
		switch fp.kind {
		case kPatch:
			e = checkKeys(fp.sub, m.Get(tfd).Message())
		case kKeyed:
			e = checkKeyed(fp, tfd, m)
		case kIndexed:
			e = checkIndexed(fp, m.Get(tfd).List())
		}
		if e != nil {
			return e.under("." + string(tfd.Name()))
		}
	}
	return nil
}

// checkKeyed checks a keyed list's or map's items.
func checkKeyed(fp *fplan, tfd protoreflect.FieldDescriptor, m protoreflect.Message) *pathError {
	if tfd.IsMap() {
		if fp.sub == nil {
			return nil
		}
		var e *pathError
		m.Get(tfd).Map().Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
			if e = checkKeys(fp.sub, v.Message()); e != nil {
				e.under(fmt.Sprintf("[%v]", k.Interface()))
			}
			return e == nil
		})
		return e
	}
	l := m.Get(tfd).List()
	keyFd := tfd.Message().Fields().ByName(fp.keySrc)
	seen := make(map[any]bool, l.Len())
	for i := 0; i < l.Len(); i++ {
		item := l.Get(i).Message()
		if keyFd.HasPresence() && !item.Has(keyFd) {
			return (&pathError{msg: fmt.Sprintf("the key %s is unset", keyFd.Name())}).under(fmt.Sprintf("[%d]", i))
		}
		k := keyOf(keyFd, item.Get(keyFd)).Interface()
		if seen[k] {
			return &pathError{msg: fmt.Sprintf("two items have the key %v", k)}
		}
		seen[k] = true
		if e := checkKeys(fp.sub, item); e != nil {
			return e.under(fmt.Sprintf("[%v]", k))
		}
	}
	return nil
}

// checkIndexed checks an indexed list's items.
func checkIndexed(fp *fplan, l protoreflect.List) *pathError {
	if fp.sub == nil {
		return nil
	}
	for i := 0; i < l.Len(); i++ {
		if e := checkKeys(fp.sub, l.Get(i).Message()); e != nil {
			return e.under(fmt.Sprintf("[%d]", i))
		}
	}
	return nil
}

// findUnknown returns the path of the first message under m with unknown
// fields.
func findUnknown(m protoreflect.Message, where string) (string, bool) {
	e := unknownIn(m)
	if e == nil {
		return "", false
	}
	var b strings.Builder
	b.WriteString(where)
	for i := len(e.steps) - 1; i >= 0; i-- {
		b.WriteString(e.steps[i])
	}
	return b.String(), true
}

// unknownIn returns the steps to the first message under m with unknown
// fields, or nil.
func unknownIn(m protoreflect.Message) *pathError {
	if len(m.GetUnknown()) > 0 {
		return &pathError{}
	}
	var e *pathError
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap():
			if fd.MapValue().Message() == nil {
				return true
			}
			v.Map().Range(func(k protoreflect.MapKey, mv protoreflect.Value) bool {
				if e = unknownIn(mv.Message()); e != nil {
					e.under(fmt.Sprintf("[%v]", k.Interface()))
				}
				return e == nil
			})
		case fd.IsList():
			if fd.Message() == nil {
				return true
			}
			for i := 0; i < v.List().Len() && e == nil; i++ {
				if e = unknownIn(v.List().Get(i).Message()); e != nil {
					e.under(fmt.Sprintf("[%d]", i))
				}
			}
		case fd.Message() != nil:
			e = unknownIn(v.Message())
		}
		if e != nil {
			e.under("." + string(fd.Name()))
		}
		return e == nil
	})
	return e
}
