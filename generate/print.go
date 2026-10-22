package generate

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/nexteggapp/protodiffgen/internal/schema"
)

type symbol struct {
	aggregate bool // a package, message, enum or service
	typ       bool // a message or enum
}

type printer struct {
	b    strings.Builder
	syms map[string]symbol
}

func (g *gen) print() []byte {
	p := &printer{syms: g.symbols()}
	p.line(0, `edition = "2023";`)
	p.line(0, "")
	p.line(0, "package %s;", g.cfg.pkg)
	p.line(0, "")
	for _, imp := range sortedKeys(g.imports) {
		p.line(0, "import %s;", quote(imp))
	}
	if fo := g.cfg.fileOptions; fo != nil {
		var fields []protoreflect.FieldDescriptor
		fo.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
			fields = append(fields, fd)
			return true
		})
		sort.Slice(fields, func(i, j int) bool { return fields[i].Number() < fields[j].Number() })
		if len(fields) > 0 {
			p.line(0, "")
		}
		for _, fd := range fields {
			name := string(fd.Name())
			if fd.IsExtension() {
				name = "(" + p.extName(fd.FullName(), g.cfg.pkg) + ")"
			}
			v := fo.Get(fd)
			if fd.IsList() {
				for i := 0; i < v.List().Len(); i++ {
					p.line(0, "option %s = %s;", name, literal(fd, v.List().Get(i)))
				}
				continue
			}
			p.line(0, "option %s = %s;", name, literal(fd, v))
		}
	}
	for _, m := range g.top {
		p.line(0, "")
		p.message(m, 0)
	}
	return []byte(p.b.String())
}

func (p *printer) line(indent int, format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	if s != "" {
		p.b.WriteString(strings.Repeat("  ", indent))
	}
	p.b.WriteString(s)
	p.b.WriteByte('\n')
}

func (p *printer) comment(indent int, lines []string) {
	for _, l := range lines {
		p.line(indent, "// %s", l)
	}
}

func (p *printer) message(m *message, indent int) {
	p.comment(indent, m.comment)
	p.line(indent, "message %s {", m.name)
	in := indent + 1
	if m.kind != schema.KindUnspecified {
		p.line(in, "option (%s) = { source: %s kind: %s };", p.extName(schema.Package+".change", m.full), quote(string(m.source)), m.kind)
		if m.top {
			p.line(0, "")
		}
	}
	open := -1
	for _, f := range m.ordered() {
		if f.oneof != open {
			if open >= 0 {
				p.line(in, "}")
			}
			if f.oneof >= 0 {
				p.line(in, "oneof %s {", m.oneofs[f.oneof])
			}
			open = f.oneof
		}
		fi := in
		if f.oneof >= 0 {
			fi++
		}
		p.field(m, f, fi)
	}
	if open >= 0 {
		p.line(in, "}")
	}
	for _, n := range m.nested {
		p.line(0, "")
		p.message(n, in)
	}
	p.line(indent, "}")
}

func (p *printer) field(m *message, f *field, indent int) {
	p.comment(indent, f.comment)
	var typ string
	if f.ref != "" {
		typ = p.typeName(f.ref, m.full, f.gen)
	} else {
		typ = f.kind.String()
	}
	if f.repeated {
		typ = "repeated " + typ
	}
	var opts []string
	for _, ft := range f.features {
		opts = append(opts, fmt.Sprintf("features.%s = %s", ft.name, ft.value))
	}
	if s := f.source; s != nil {
		parts := []string{"source: " + quote(string(s.Source()))}
		if s.Key() {
			parts = append(parts, "key: true")
		}
		if s.Order() {
			parts = append(parts, "order: true")
		}
		if s.Index() {
			parts = append(parts, "index: true")
		}
		opts = append(opts, fmt.Sprintf("(%s) = { %s }", p.extName(schema.Package+".field", m.full), strings.Join(parts, " ")))
	}
	suffix := ""
	if len(opts) > 0 {
		suffix = " [" + strings.Join(opts, ", ") + "]"
	}
	p.line(indent, "%s %s = %d%s;", typ, f.name, f.number, suffix)
}

// typeName returns the name to print for a type used in scope. A generated
// type gets the shortest name that resolves to it, and a source type its full
// name, with a leading dot only when something in scope would shadow it.
func (p *printer) typeName(full, scope protoreflect.FullName, generated bool) string {
	if generated {
		parts := strings.Split(string(full), ".")
		for k := 1; k <= len(parts); k++ {
			cand := strings.Join(parts[len(parts)-k:], ".")
			if got, ok := p.lookup(cand, string(scope), true); ok && got == string(full) {
				return cand
			}
		}
	} else if got, ok := p.lookup(string(full), string(scope), true); ok && got == string(full) {
		return string(full)
	}
	return "." + string(full)
}

func (p *printer) extName(full, scope protoreflect.FullName) string {
	if got, ok := p.lookup(string(full), string(scope), false); ok && got == string(full) {
		return string(full)
	}
	return "." + string(full)
}

// lookup resolves a name used in scope the way protoc does.
func (p *printer) lookup(name, scope string, types bool) (string, bool) {
	first, _, compound := strings.Cut(name, ".")
	for s := scope; s != ""; {
		cand := s + "." + first
		if sym, ok := p.syms[cand]; ok {
			if compound {
				if sym.aggregate {
					full := s + "." + name
					_, ok := p.syms[full]
					return full, ok
				}
			} else if sym.typ || !types {
				return cand, true
			}
		}
		if i := strings.LastIndexByte(s, '.'); i >= 0 {
			s = s[:i]
		} else {
			s = ""
		}
	}
	_, ok := p.syms[name]
	return name, ok
}

// symbols returns every name protoc could resolve while compiling the
// generated file.
func (g *gen) symbols() map[string]symbol {
	syms := map[string]symbol{}
	addPkg := func(pkg protoreflect.FullName) {
		for pkg != "" {
			syms[string(pkg)] = symbol{aggregate: true}
			pkg = pkg.Parent()
		}
	}
	var addMsg func(md protoreflect.MessageDescriptor)
	addEnum := func(ed protoreflect.EnumDescriptor) {
		syms[string(ed.FullName())] = symbol{aggregate: true, typ: true}
		for i := 0; i < ed.Values().Len(); i++ {
			syms[string(ed.Values().Get(i).FullName())] = symbol{}
		}
	}
	addExts := func(xs protoreflect.ExtensionDescriptors) {
		for i := 0; i < xs.Len(); i++ {
			syms[string(xs.Get(i).FullName())] = symbol{}
		}
	}
	addMsg = func(md protoreflect.MessageDescriptor) {
		syms[string(md.FullName())] = symbol{aggregate: true, typ: true}
		for i := 0; i < md.Fields().Len(); i++ {
			syms[string(md.Fields().Get(i).FullName())] = symbol{}
		}
		for i := 0; i < md.Oneofs().Len(); i++ {
			syms[string(md.Oneofs().Get(i).FullName())] = symbol{}
		}
		for i := 0; i < md.Messages().Len(); i++ {
			addMsg(md.Messages().Get(i))
		}
		for i := 0; i < md.Enums().Len(); i++ {
			addEnum(md.Enums().Get(i))
		}
		addExts(md.Extensions())
	}
	g.files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		addPkg(fd.Package())
		for i := 0; i < fd.Messages().Len(); i++ {
			addMsg(fd.Messages().Get(i))
		}
		for i := 0; i < fd.Enums().Len(); i++ {
			addEnum(fd.Enums().Get(i))
		}
		addExts(fd.Extensions())
		for i := 0; i < fd.Services().Len(); i++ {
			sd := fd.Services().Get(i)
			syms[string(sd.FullName())] = symbol{aggregate: true}
			for j := 0; j < sd.Methods().Len(); j++ {
				syms[string(sd.Methods().Get(j).FullName())] = symbol{}
			}
		}
		return true
	})
	addPkg(g.cfg.pkg)
	var addGen func(m *message)
	addGen = func(m *message) {
		syms[string(m.full)] = symbol{aggregate: true, typ: true}
		for _, f := range m.fields {
			syms[string(m.full)+"."+f.name] = symbol{}
		}
		for _, o := range m.oneofs {
			syms[string(m.full)+"."+o] = symbol{}
		}
		for _, n := range m.nested {
			addGen(n)
		}
	}
	for _, m := range g.top {
		addGen(m)
	}
	return syms
}

// literal returns a value in .proto option syntax.
func literal(fd protoreflect.FieldDescriptor, v protoreflect.Value) string {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return "{ " + aggregate(v.Message()) + " }"
	}
	return scalar(fd, v)
}

func aggregate(m protoreflect.Message) string {
	var fields []protoreflect.FieldDescriptor
	m.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		fields = append(fields, fd)
		return true
	})
	sort.Slice(fields, func(i, j int) bool { return fields[i].Number() < fields[j].Number() })
	var parts []string
	for _, fd := range fields {
		name := string(fd.Name())
		if fd.IsExtension() {
			name = "[" + string(fd.FullName()) + "]"
		}
		v := m.Get(fd)
		one := func(fd protoreflect.FieldDescriptor, v protoreflect.Value) {
			if fd.Message() != nil {
				parts = append(parts, name+" { "+aggregate(v.Message())+" }")
			} else {
				parts = append(parts, name+": "+scalar(fd, v))
			}
		}
		switch {
		case fd.IsMap():
			var keys []protoreflect.MapKey
			v.Map().Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
				keys = append(keys, k)
				return true
			})
			sort.Slice(keys, func(i, j int) bool { return lessKey(keys[i], keys[j]) })
			for _, k := range keys {
				entry := name + " { key: " + scalar(fd.MapKey(), k.Value()) + " value"
				mv := v.Map().Get(k)
				if fd.MapValue().Message() != nil {
					entry += " { " + aggregate(mv.Message()) + " } }"
				} else {
					entry += ": " + scalar(fd.MapValue(), mv) + " }"
				}
				parts = append(parts, entry)
			}
		case fd.IsList():
			for i := 0; i < v.List().Len(); i++ {
				one(fd, v.List().Get(i))
			}
		default:
			one(fd, v)
		}
	}
	return strings.Join(parts, " ")
}

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

func scalar(fd protoreflect.FieldDescriptor, v protoreflect.Value) string {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return quote(v.String())
	case protoreflect.BytesKind:
		return quote(string(v.Bytes()))
	case protoreflect.BoolKind:
		return strconv.FormatBool(v.Bool())
	case protoreflect.EnumKind:
		if ev := fd.Enum().Values().ByNumber(v.Enum()); ev != nil {
			return string(ev.Name())
		}
		return strconv.Itoa(int(v.Enum()))
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		f := v.Float()
		switch {
		case math.IsNaN(f):
			return "nan"
		case math.IsInf(f, 1):
			return "inf"
		case math.IsInf(f, -1):
			return "-inf"
		}
		bits := 64
		if fd.Kind() == protoreflect.FloatKind {
			bits = 32
		}
		return strconv.FormatFloat(f, 'g', -1, bits)
	case protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind,
		protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		return strconv.FormatInt(v.Int(), 10)
	}
	return strconv.FormatUint(v.Uint(), 10)
}

// quote returns s as a .proto string literal, escaping everything but
// printable ASCII.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20 || c >= 0x7f:
			fmt.Fprintf(&b, `\%03o`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
