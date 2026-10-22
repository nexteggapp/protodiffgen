// Package generate writes the diff .proto for a target message from a
// DiffConfig.
package generate

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/nexteggapp/protodiffgen/internal/schema"
)

const emptyPath = "google/protobuf/empty.proto"

// File returns the diff .proto for the target a DiffConfig names. set holds
// the target's file and every file it imports; config is the DiffConfig in
// text format. A mistake in the config is returned as an error that names
// the config entry or the source field.
func File(set *descriptorpb.FileDescriptorSet, config []byte) ([]byte, error) {
	out, err := run(set, config)
	if err != nil {
		return nil, err
	}
	return out.text, nil
}

type output struct {
	text []byte
	desc *descriptorpb.FileDescriptorProto
}

func run(set *descriptorpb.FileDescriptorSet, text []byte) (*output, error) {
	files, err := protodesc.NewFiles(dedupe(set))
	if err != nil {
		return nil, fmt.Errorf("descriptor set: %v", err)
	}
	for _, fd := range []protoreflect.FileDescriptor{schema.File(), emptypb.File_google_protobuf_empty_proto} {
		if _, err := files.FindFileByPath(fd.Path()); err != nil {
			if err := files.RegisterFile(fd); err != nil {
				return nil, fmt.Errorf("descriptor set: %v", err)
			}
		}
	}

	cfg, errs := parseConfig(text, files)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	g := &gen{
		files:   files,
		cfg:     cfg,
		bodies:  map[protoreflect.FullName]*message{},
		names:   map[string]protoreflect.FullName{},
		imports: map[string]bool{schema.Path: true},
	}
	if g.atomicType(cfg.target) {
		return nil, fmt.Errorf("config: target_message: %s is atomic", cfg.target.FullName())
	}
	g.body(cfg.target)
	g.markKeys()
	for _, m := range g.top {
		g.checkNames(m)
	}
	if fo := cfg.fileOptions; fo != nil {
		fo.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
			if fd.IsExtension() {
				g.imports[fd.ParentFile().Path()] = true
			}
			return true
		})
	}
	if len(g.errs) > 0 {
		return nil, errors.Join(g.errs...)
	}

	desc := g.descriptor()
	if _, err := protodesc.NewFile(desc, files); err != nil {
		return nil, fmt.Errorf("the generated file is invalid: %v", err)
	}
	return &output{text: g.print(), desc: desc}, nil
}

// dedupe drops repeated files, which appear when several descriptor sets are
// concatenated.
func dedupe(set *descriptorpb.FileDescriptorSet) *descriptorpb.FileDescriptorSet {
	seen := map[string]bool{}
	out := &descriptorpb.FileDescriptorSet{}
	for _, f := range set.GetFile() {
		if !seen[f.GetName()] {
			seen[f.GetName()] = true
			out.File = append(out.File, f)
		}
	}
	return out
}

// message is a generated message.
type message struct {
	name    string
	full    protoreflect.FullName
	source  protoreflect.FullName
	kind    schema.Kind // 0 for a message with no change option
	comment []string
	fields  []*field
	oneofs  []string
	nested  []*message
	top     bool
	origin  string // what made it, for errors
}

// field is a generated field.
type field struct {
	name     string
	number   protoreflect.FieldNumber
	repeated bool
	kind     protoreflect.Kind
	ref      protoreflect.FullName // message or enum type
	gen      bool                  // ref is a generated message
	oneof    int                   // index in message.oneofs, or -1
	comment  []string
	source   *schema.FieldSource
	features []feature
	origin   string
}

type feature struct {
	name  string // such as "utf8_validation"
	value string // such as "NONE"
}

type gen struct {
	files   *protoregistry.Files
	cfg     *config
	errs    []error
	bodies  map[protoreflect.FullName]*message
	names   map[string]protoreflect.FullName
	top     []*message
	imports map[string]bool
	keyed   []*rule
}

func (g *gen) errorf(format string, args ...any) {
	g.errs = append(g.errs, fmt.Errorf(format, args...))
}

func (g *gen) atomicType(md protoreflect.MessageDescriptor) bool {
	return strings.HasPrefix(string(md.FullName()), "google.protobuf.") || g.cfg.atomicTypes[md.FullName()]
}

// body returns the patch body for md, making it on first use.
func (g *gen) body(md protoreflect.MessageDescriptor) *message {
	if m := g.bodies[md.FullName()]; m != nil {
		return m
	}
	name := strings.ReplaceAll(strings.TrimPrefix(string(md.FullName()), string(md.ParentFile().Package())+"."), ".", "_") + "Diff"
	m := &message{
		name:   name,
		full:   g.cfg.pkg.Append(protoreflect.Name(name)),
		source: md.FullName(),
		kind:   schema.KindPatch,
		top:    true,
		origin: "the patch body for " + string(md.FullName()),
	}
	g.bodies[md.FullName()] = m
	if prev, ok := g.names[name]; ok {
		g.errorf("%s and %s both get the patch body name %s", prev, md.FullName(), name)
	} else {
		g.names[name] = md.FullName()
		if _, err := g.files.FindDescriptorByName(m.full); err == nil {
			g.errorf("%s: its patch body %s collides with an existing %s", md.FullName(), m.full, m.full)
		}
	}
	g.top = append(g.top, m)
	g.fill(m, md)
	return m
}

func (g *gen) fill(m *message, md protoreflect.MessageDescriptor) {
	var ordered []*rule
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Kind() == protoreflect.GroupKind {
			g.errorf("%s: groups are not supported", fd.FullName())
			continue
		}
		f := &field{
			name:   string(fd.Name()),
			number: fd.Number(),
			oneof:  -1,
			source: ptr(schema.NewFieldSource(fd.FullName(), false, false, false)),
			origin: "field " + string(fd.FullName()),
		}
		if o := fd.ContainingOneof(); o != nil && !o.IsSynthetic() {
			f.oneof = m.oneof(string(o.Name()))
		}
		switch {
		case fd.IsMap():
			g.mapField(m, f, fd)
		case fd.IsList():
			r := g.cfg.rules[fd.FullName()]
			if fd.Message() != nil && r == nil {
				g.errorf("%s: repeated message field has no list rule", fd.FullName())
				continue
			}
			strategy := int32(schema.StrategyReplace)
			if r != nil {
				strategy = r.strategy
			}
			switch strategy {
			case schema.StrategyKeyed:
				g.keyedField(m, f, fd, r)
				if r.ordered {
					ordered = append(ordered, r)
				}
			case schema.StrategyIndexed:
				g.indexedField(m, f, fd)
			default:
				g.replaceField(m, f, fd)
			}
		case fd.Message() != nil:
			if g.atomicType(fd.Message()) || g.cfg.atomicFields[fd.FullName()] {
				g.setType(f, fd)
				f.comment = []string{`Atomic: present means "set to this".`}
			} else {
				g.genType(f, g.body(fd.Message()))
			}
		default:
			g.setType(f, fd)
			f.features = features(fd)
		}
		m.fields = append(m.fields, f)
	}

	number := protoreflect.FieldNumber(schema.FirstOrderNumber)
	for _, r := range ordered {
		f := &field{
			name:     string(r.field.Name()) + "_order",
			number:   number,
			repeated: true,
			oneof:    -1,
			comment:  []string{fmt.Sprintf("The %s keys in their final order. Present only when it changed.", possessive(string(r.field.Name())))},
			source:   ptr(schema.NewFieldSource(r.field.FullName(), false, true, false)),
			features: features(r.key),
			origin:   "the order field of " + string(r.field.FullName()),
		}
		g.setType(f, r.key)
		m.fields = append(m.fields, f)
		number--
	}
	m.fields = append(m.fields, &field{
		name:     g.cfg.clearName,
		number:   schema.ClearNumber,
		repeated: true,
		kind:     protoreflect.Int32Kind,
		oneof:    -1,
		comment:  []string{"Numbers of fields that become unset."},
		origin:   "the clear field",
	})
	low := number + 1
	for i := 0; i < fields.Len(); i++ {
		if fd := fields.Get(i); fd.Number() >= low {
			g.errorf("%s: number %d is in the range generated fields use in %s (%d and up)", fd.FullName(), fd.Number(), m.name, low)
		}
	}
}

func (g *gen) keyedField(m *message, f *field, fd protoreflect.FieldDescriptor, r *rule) {
	item := fd.Message()
	w := g.wrapper(m, fd, "Change", schema.KindListChange)
	key := &field{
		name:     string(r.key.Name()),
		number:   1,
		oneof:    -1,
		source:   ptr(schema.NewFieldSource(r.key.FullName(), true, false, false)),
		features: features(r.key),
		origin:   "the key field " + string(r.key.FullName()),
	}
	g.setType(key, r.key)
	op := w.oneof("op")
	add := &field{name: "add", number: 2, oneof: op, origin: "the add field"}
	g.refMessage(add, item)
	patch := &field{name: "patch", number: 4, oneof: op, origin: "the patch field"}
	if g.atomicType(item) {
		g.refMessage(patch, item)
	} else {
		g.genType(patch, g.body(item))
		g.keyed = append(g.keyed, r)
	}
	w.fields = []*field{key, add, g.removeField(op), patch}
	f.repeated = true
	g.genType(f, w)
}

func (g *gen) mapField(m *message, f *field, fd protoreflect.FieldDescriptor) {
	k, v := fd.MapKey(), fd.MapValue()
	w := g.wrapper(m, fd, "Change", schema.KindListChange)
	key := &field{
		name:     "key",
		number:   1,
		oneof:    -1,
		source:   ptr(schema.NewFieldSource(k.FullName(), true, false, false)),
		features: features(k),
		origin:   "the map key",
	}
	g.setType(key, k)
	op := w.oneof("op")
	if v.Message() != nil && !g.atomicType(v.Message()) {
		add := &field{name: "add", number: 2, oneof: op, origin: "the add field"}
		g.refMessage(add, v.Message())
		patch := &field{name: "patch", number: 4, oneof: op, origin: "the patch field"}
		g.genType(patch, g.body(v.Message()))
		w.fields = []*field{key, add, g.removeField(op), patch}
	} else {
		set := &field{name: "set", number: 2, oneof: op, features: features(v), origin: "the set field"}
		g.setType(set, v)
		w.fields = []*field{key, set, g.removeField(op)}
	}
	f.repeated = true
	g.genType(f, w)
}

func (g *gen) indexedField(m *message, f *field, fd protoreflect.FieldDescriptor) {
	w := g.wrapper(m, fd, "Edit", schema.KindListEdit)
	index := &field{
		name:    "index",
		number:  1,
		kind:    protoreflect.Uint32Kind,
		oneof:   -1,
		comment: []string{"A position in the base list of n items: [0, n) for patch and remove,", "[0, n] for insert_before, where n appends."},
		source:  ptr(schema.NewFieldSource(fd.FullName(), false, false, true)),
		origin:  "the index field",
	}
	op := w.oneof("op")
	patch := &field{name: "patch", number: 2, oneof: op, origin: "the patch field"}
	switch {
	case fd.Message() == nil:
		g.setType(patch, fd)
		patch.features = valueFeatures(fd)
	case g.atomicType(fd.Message()):
		g.refMessage(patch, fd.Message())
	default:
		g.genType(patch, g.body(fd.Message()))
	}
	inserted := &message{
		name:    "Inserted",
		full:    w.full.Append("Inserted"),
		comment: []string{"The elements inserted, in order."},
		origin:  "the Inserted message for " + string(fd.FullName()),
	}
	items := &field{name: "items", number: 1, repeated: true, oneof: -1, features: features(fd), origin: "the items field"}
	g.setType(items, fd)
	inserted.fields = []*field{items}
	w.nested = []*message{inserted}
	insert := &field{name: "insert_before", number: 4, oneof: op, origin: "the insert_before field"}
	g.genType(insert, inserted)
	w.fields = []*field{index, patch, g.removeField(op), insert}
	f.repeated = true
	g.genType(f, w)
}

func (g *gen) replaceField(m *message, f *field, fd protoreflect.FieldDescriptor) {
	w := g.wrapper(m, fd, "Replace", schema.KindReplace)
	w.comment = []string{`Present means "use this list".`}
	list := &field{name: string(fd.Name()), number: 1, repeated: true, oneof: -1, features: features(fd), origin: "the list field"}
	g.setType(list, fd)
	w.fields = []*field{list}
	g.genType(f, w)
}

func (g *gen) wrapper(m *message, fd protoreflect.FieldDescriptor, suffix string, kind schema.Kind) *message {
	name := camel(string(fd.Name())) + suffix
	w := &message{
		name:   name,
		full:   m.full.Append(protoreflect.Name(name)),
		source: fd.FullName(),
		kind:   kind,
		origin: fmt.Sprintf("the message %s made for %s", name, fd.FullName()),
	}
	m.nested = append(m.nested, w)
	return w
}

func (g *gen) removeField(op int) *field {
	f := &field{name: "remove", number: 3, oneof: op, origin: "the remove field"}
	g.refMessage(f, emptypb.File_google_protobuf_empty_proto.Messages().ByName("Empty"))
	return f
}

// setType gives f the type of a source field's values.
func (g *gen) setType(f *field, fd protoreflect.FieldDescriptor) {
	f.kind = fd.Kind()
	switch {
	case fd.Message() != nil:
		g.refMessage(f, fd.Message())
	case fd.Enum() != nil:
		f.ref = fd.Enum().FullName()
		g.imports[fd.Enum().ParentFile().Path()] = true
	}
}

func (g *gen) refMessage(f *field, md protoreflect.MessageDescriptor) {
	f.kind = protoreflect.MessageKind
	f.ref = md.FullName()
	g.imports[md.ParentFile().Path()] = true
}

func (g *gen) genType(f *field, m *message) {
	f.kind = protoreflect.MessageKind
	f.ref = m.full
	f.gen = true
}

func (m *message) oneof(name string) int {
	for i, n := range m.oneofs {
		if n == name {
			return i
		}
	}
	m.oneofs = append(m.oneofs, name)
	return len(m.oneofs) - 1
}

// markKeys comments the key field in each keyed item's patch body.
func (g *gen) markKeys() {
	for _, r := range g.keyed {
		m := g.bodies[r.field.Message().FullName()]
		for _, f := range m.fields {
			if f.name == string(r.key.Name()) && f.comment == nil {
				item := r.field.Message()
				f.comment = []string{fmt.Sprintf("Kept, so %s serves wherever %s appears. A patch may not change it.", m.name, item.Name())}
			}
		}
	}
}

// checkNames fails on two names, or two JSON names, that collide in one
// message.
func (g *gen) checkNames(m *message) {
	names := map[string]string{}
	jsons := map[string]string{}
	add := func(name, origin string) {
		if prev, ok := names[name]; ok {
			g.errorf("%s: in %s, %s and %s both use the name %q", m.source, m.full, prev, origin, name)
			return
		}
		names[name] = origin
	}
	for _, f := range m.fields {
		add(f.name, f.origin)
		j := schema.JSONName(f.name)
		if prev, ok := jsons[j]; ok && names[f.name] == f.origin {
			g.errorf("%s: in %s, %s and %s both have the JSON name %q", m.source, m.full, prev, f.origin, j)
		}
		jsons[j] = f.origin
	}
	for _, o := range m.oneofs {
		add(o, "the oneof "+o)
	}
	for _, n := range m.nested {
		add(n.name, n.origin)
		g.checkNames(n)
	}
}

// features returns the features a generated field copies from a source
// field: those that differ from edition 2023's defaults, except presence.
func features(fd protoreflect.FieldDescriptor) []feature {
	out := valueFeatures(fd)
	if fd.IsList() && !fd.IsPacked() && packable(fd.Kind()) {
		out = append(out, feature{"repeated_field_encoding", "EXPANDED"})
	}
	return out
}

// valueFeatures returns the features that apply to one value of a source
// field, for a singular generated field that holds one element of a list.
func valueFeatures(fd protoreflect.FieldDescriptor) []feature {
	if fd.Kind() == protoreflect.StringKind && !utf8Checked(fd) {
		return []feature{{"utf8_validation", "NONE"}}
	}
	return nil
}

func packable(k protoreflect.Kind) bool {
	switch k {
	case protoreflect.StringKind, protoreflect.BytesKind, protoreflect.MessageKind, protoreflect.GroupKind:
		return false
	}
	return true
}

// utf8Checked reports whether a source string field checks UTF-8.
func utf8Checked(fd protoreflect.FieldDescriptor) bool {
	switch fd.ParentFile().Syntax() {
	case protoreflect.Proto2:
		return false
	case protoreflect.Proto3:
		return true
	}
	if v, ok := utf8Feature(fd.Options()); ok {
		return v
	}
	// A map entry's fields take the map field's features.
	parent := fd.Parent()
	if md, ok := parent.(protoreflect.MessageDescriptor); ok && md.IsMapEntry() {
		if outer, ok := md.Parent().(protoreflect.MessageDescriptor); ok {
			outerFields := outer.Fields()
			for i := 0; i < outerFields.Len(); i++ {
				if mf := outerFields.Get(i); mf.Message() == md {
					if v, ok := utf8Feature(mf.Options()); ok {
						return v
					}
				}
			}
		}
	}
	for d := parent; d != nil; d = d.Parent() {
		switch d := d.(type) {
		case protoreflect.MessageDescriptor:
			if v, ok := utf8Feature(d.Options()); ok {
				return v
			}
		case protoreflect.FileDescriptor:
			if v, ok := utf8Feature(d.Options()); ok {
				return v
			}
			return true
		}
	}
	return true
}

func utf8Feature(opts any) (bool, bool) {
	type featured interface {
		GetFeatures() *descriptorpb.FeatureSet
	}
	o, ok := opts.(featured)
	if !ok || o.GetFeatures() == nil || o.GetFeatures().Utf8Validation == nil {
		return false, false
	}
	return o.GetFeatures().GetUtf8Validation() == descriptorpb.FeatureSet_VERIFY, true
}

// camel turns a field name into the start of a message name: "tax_lots"
// gives "TaxLots".
func camel(s string) string {
	var b strings.Builder
	up := true
	for _, c := range s {
		if c == '_' {
			up = true
			continue
		}
		if up && 'a' <= c && c <= 'z' {
			c -= 'a' - 'A'
		}
		up = false
		b.WriteRune(c)
	}
	return b.String()
}

func possessive(s string) string {
	if strings.HasSuffix(s, "s") {
		return s + "'"
	}
	return s + "'s"
}

func ptr[T any](v T) *T { return &v }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
