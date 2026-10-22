package apply

import (
	"bytes"
	"math"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Equal reports whether x and y are equal under the list rules of diff, a
// message of the diff type; diff's contents are ignored.
//
// An UNORDERED keyed list compares as a set by key, an ORDERED one in order,
// and INDEXED and REPLACE lists and maps element by element, recursively.
// Floats compare by their bits. Extension fields are not compared.
func Equal(x, y, diff proto.Message) bool {
	return rulesFor(diff.ProtoReflect().Descriptor()).equal(x.ProtoReflect(), y.ProtoReflect())
}

func (r rules) equal(x, y protoreflect.Message) bool {
	if x.Descriptor().FullName() != y.Descriptor().FullName() {
		return false
	}
	p := r[x.Descriptor().FullName()]
	fields := x.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		yfd := y.Descriptor().Fields().ByNumber(fd.Number())
		if yfd == nil {
			return false
		}
		if fd.HasPresence() && x.Has(fd) != y.Has(yfd) {
			return false
		}
		xv, yv := x.Get(fd), y.Get(yfd)
		switch {
		case fd.IsMap():
			if !r.equalMap(fd, xv.Map(), yv.Map()) {
				return false
			}
		case fd.IsList():
			var fp *fplan
			if p != nil {
				fp = p.byNum[fd.Number()]
			}
			if fp != nil && fp.kind == kKeyed && fp.order == nil {
				if !r.equalSet(fd, fp.keySrc, xv.List(), yv.List()) {
					return false
				}
			} else if !r.equalList(fd, xv.List(), yv.List()) {
				return false
			}
		default:
			if !r.equalValue(fd, xv, yv) {
				return false
			}
		}
	}
	return bytes.Equal(x.GetUnknown(), y.GetUnknown())
}

func (r rules) equalValue(fd protoreflect.FieldDescriptor, x, y protoreflect.Value) bool {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return r.equal(x.Message(), y.Message())
	case protoreflect.FloatKind:
		return math.Float32bits(float32(x.Float())) == math.Float32bits(float32(y.Float()))
	case protoreflect.DoubleKind:
		return math.Float64bits(x.Float()) == math.Float64bits(y.Float())
	case protoreflect.BytesKind:
		return bytes.Equal(x.Bytes(), y.Bytes())
	}
	return x.Interface() == y.Interface()
}

func (r rules) equalList(fd protoreflect.FieldDescriptor, x, y protoreflect.List) bool {
	if x.Len() != y.Len() {
		return false
	}
	for i := 0; i < x.Len(); i++ {
		if !r.equalValue(fd, x.Get(i), y.Get(i)) {
			return false
		}
	}
	return true
}

func (r rules) equalMap(fd protoreflect.FieldDescriptor, x, y protoreflect.Map) bool {
	if x.Len() != y.Len() {
		return false
	}
	eq := true
	x.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		if !y.Has(k) || !r.equalValue(fd.MapValue(), v, y.Get(k)) {
			eq = false
		}
		return eq
	})
	return eq
}

// equalSet compares two keyed lists as sets by key. Lists with a repeated or
// unset key compare element by element.
func (r rules) equalSet(fd protoreflect.FieldDescriptor, key protoreflect.Name, x, y protoreflect.List) bool {
	if x.Len() != y.Len() {
		return false
	}
	keyFd := fd.Message().Fields().ByName(key)
	if keyFd == nil {
		return r.equalList(fd, x, y)
	}
	xm, okx := indexKeys(x, keyFd)
	ym, oky := indexKeys(y, keyFd)
	if !okx || !oky {
		return r.equalList(fd, x, y)
	}
	for k, i := range xm {
		j, ok := ym[k]
		if !ok || !r.equal(x.Get(i).Message(), y.Get(j).Message()) {
			return false
		}
	}
	return true
}

// indexKeys maps each item's key to its position. It reports false if a key
// is unset or repeated.
func indexKeys(l protoreflect.List, keyFd protoreflect.FieldDescriptor) (map[any]int, bool) {
	m := make(map[any]int, l.Len())
	for i := 0; i < l.Len(); i++ {
		item := l.Get(i).Message()
		if keyFd.HasPresence() && !item.Has(keyFd) {
			return nil, false
		}
		k := keyOf(keyFd, item.Get(keyFd)).Interface()
		if _, dup := m[k]; dup {
			return nil, false
		}
		m[k] = i
	}
	return m, true
}

// keyOf returns a key value as a MapKey. An enum key is held as its number.
func keyOf(fd protoreflect.FieldDescriptor, v protoreflect.Value) protoreflect.MapKey {
	if fd.Kind() == protoreflect.EnumKind {
		return protoreflect.ValueOfInt32(int32(v.Enum())).MapKey()
	}
	return v.MapKey()
}

// keyValue turns a MapKey back into a value of field fd.
func keyValue(fd protoreflect.FieldDescriptor, k protoreflect.MapKey) protoreflect.Value {
	if fd.Kind() == protoreflect.EnumKind {
		return protoreflect.ValueOfEnum(protoreflect.EnumNumber(k.Int()))
	}
	return k.Value()
}

// lessKey orders keys: strings by bytes, numbers by value, false before true.
func lessKey(a, b protoreflect.MapKey) bool {
	switch x := a.Interface().(type) {
	case string:
		return x < b.String()
	case bool:
		return !x && b.Bool()
	case int32, int64:
		return a.Int() < b.Int()
	}
	return a.Uint() < b.Uint()
}
