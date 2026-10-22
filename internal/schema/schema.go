// Package schema holds the descriptor of nextgui.xyz/protodiffgen/v1/config.proto,
// built in Go so that the library needs no generated code, and reads and writes
// the options protodiffgen puts on generated messages and fields.
package schema

import (
	"fmt"
	"sync"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	// Path is the import path of config.proto.
	Path = "nextgui.xyz/protodiffgen/v1/config.proto"
	// Package is the proto package of config.proto.
	Package = "nextgui.protodiffgen.v1"
	// ExtensionNumber is the number of both the change and the field option.
	ExtensionNumber = 52000
	// ClearNumber is the number of every patch body's clear field.
	ClearNumber = 536870911
	// FirstOrderNumber is the number of a patch body's first order field.
	// Later ones count down from it.
	FirstOrderNumber = 536870910
)

// Kind is Change.Kind: what a generated message is.
type Kind int32

// The values of Change.Kind.
const (
	KindUnspecified Kind = 0
	KindPatch       Kind = 1
	KindListChange  Kind = 2
	KindReplace     Kind = 3
	KindListEdit    Kind = 4
)

func (k Kind) String() string {
	switch k {
	case KindPatch:
		return "PATCH"
	case KindListChange:
		return "LIST_CHANGE"
	case KindReplace:
		return "REPLACE"
	case KindListEdit:
		return "LIST_EDIT"
	}
	return "KIND_UNSPECIFIED"
}

// The values of ListRule.Strategy and ListRule.Order.
const (
	StrategyUnspecified = 0
	StrategyKeyed       = 1
	StrategyReplace     = 2
	StrategyIndexed     = 3

	OrderUnspecified = 0
	OrderOrdered     = 1
	OrderUnordered   = 2
)

var (
	once     sync.Once
	file     protoreflect.FileDescriptor
	fileErr  error
	fileDesc *descriptorpb.FileDescriptorProto
)

// File returns the descriptor of config.proto. It is not registered in
// protoregistry.GlobalFiles, so it never conflicts with generated code.
func File() protoreflect.FileDescriptor {
	once.Do(func() {
		fileDesc = build()
		file, fileErr = protodesc.NewFile(fileDesc, protoregistry.GlobalFiles)
	})
	if fileErr != nil {
		panic(fmt.Sprintf("protodiffgen: config.proto descriptor: %v", fileErr))
	}
	return file
}

// FileProto returns a copy of the FileDescriptorProto of config.proto.
func FileProto() *descriptorpb.FileDescriptorProto {
	File()
	return proto.Clone(fileDesc).(*descriptorpb.FileDescriptorProto)
}

// DiffConfig returns the descriptor of nextgui.protodiffgen.v1.DiffConfig.
func DiffConfig() protoreflect.MessageDescriptor {
	return File().Messages().ByName("DiffConfig")
}

func build() *descriptorpb.FileDescriptorProto {
	const (
		str  = descriptorpb.FieldDescriptorProto_TYPE_STRING
		bln  = descriptorpb.FieldDescriptorProto_TYPE_BOOL
		msg  = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
		enm  = descriptorpb.FieldDescriptorProto_TYPE_ENUM
		one  = descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
		many = descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	)
	f := func(name string, num int32, label descriptorpb.FieldDescriptorProto_Label, typ descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
		d := &descriptorpb.FieldDescriptorProto{
			Name:     proto.String(name),
			Number:   proto.Int32(num),
			Label:    label.Enum(),
			Type:     typ.Enum(),
			JsonName: proto.String(jsonName(name)),
		}
		if typeName != "" {
			d.TypeName = proto.String(typeName)
		}
		return d
	}
	e := func(name string, values ...any) *descriptorpb.EnumDescriptorProto {
		d := &descriptorpb.EnumDescriptorProto{Name: proto.String(name)}
		for i := 0; i < len(values); i += 2 {
			d.Value = append(d.Value, &descriptorpb.EnumValueDescriptorProto{
				Name:   proto.String(values[i].(string)),
				Number: proto.Int32(int32(values[i+1].(int))),
			})
		}
		return d
	}
	ext := func(name, extendee, typeName string) *descriptorpb.FieldDescriptorProto {
		d := f(name, ExtensionNumber, one, msg, typeName)
		d.Extendee = proto.String(extendee)
		return d
	}
	const p = "." + Package + "."
	return &descriptorpb.FileDescriptorProto{
		Name:       proto.String(Path),
		Package:    proto.String(Package),
		Dependency: []string{"google/protobuf/descriptor.proto"},
		Syntax:     proto.String("proto3"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("github.com/nexteggapp/protodiffgen/v1;protodiffgenv1"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("DiffConfig"),
				Field: []*descriptorpb.FieldDescriptorProto{
					f("target_message", 1, one, str, ""),
					f("output_package", 2, one, str, ""),
					f("file_options", 3, one, msg, ".google.protobuf.FileOptions"),
					f("atomic_types", 4, many, str, ""),
					f("atomic_fields", 5, many, str, ""),
					f("lists", 6, many, msg, p+"ListRule"),
					f("clear_field_name", 7, one, str, ""),
				},
			},
			{
				Name: proto.String("ListRule"),
				Field: []*descriptorpb.FieldDescriptorProto{
					f("field", 1, one, str, ""),
					f("strategy", 2, one, enm, p+"ListRule.Strategy"),
					f("key", 3, one, str, ""),
					f("order", 4, one, enm, p+"ListRule.Order"),
				},
				EnumType: []*descriptorpb.EnumDescriptorProto{
					e("Strategy", "STRATEGY_UNSPECIFIED", 0, "KEYED", 1, "INDEXED", 3, "REPLACE", 2),
					e("Order", "ORDER_UNSPECIFIED", 0, "ORDERED", 1, "UNORDERED", 2),
				},
			},
			{
				Name: proto.String("Change"),
				Field: []*descriptorpb.FieldDescriptorProto{
					f("source", 1, one, str, ""),
					f("kind", 2, one, enm, p+"Change.Kind"),
				},
				EnumType: []*descriptorpb.EnumDescriptorProto{
					e("Kind", "KIND_UNSPECIFIED", 0, "PATCH", 1, "LIST_CHANGE", 2, "REPLACE", 3, "LIST_EDIT", 4),
				},
			},
			{
				Name: proto.String("FieldSource"),
				Field: []*descriptorpb.FieldDescriptorProto{
					f("source", 1, one, str, ""),
					f("key", 2, one, bln, ""),
					f("order", 3, one, bln, ""),
					f("index", 4, one, bln, ""),
				},
			},
		},
		Extension: []*descriptorpb.FieldDescriptorProto{
			ext("change", ".google.protobuf.MessageOptions", p+"Change"),
			ext("field", ".google.protobuf.FieldOptions", p+"FieldSource"),
		},
	}
}

func jsonName(s string) string {
	var b []byte
	up := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			up = true
			continue
		}
		if up && 'a' <= c && c <= 'z' {
			c -= 'a' - 'A'
		}
		up = false
		b = append(b, c)
	}
	return string(b)
}

// JSONName returns protoc's default JSON name for a field name.
func JSONName(s string) string { return jsonName(s) }

// FieldSource is the field option of a generated field.
type FieldSource struct {
	source protoreflect.FullName
	key    bool
	order  bool
	index  bool
}

// NewFieldSource returns a FieldSource.
func NewFieldSource(source protoreflect.FullName, key, order, index bool) FieldSource {
	return FieldSource{source: source, key: key, order: order, index: index}
}

// Source is the full name of the source field.
func (s FieldSource) Source() protoreflect.FullName { return s.source }

// Key reports whether the field is a change wrapper's key.
func (s FieldSource) Key() bool { return s.key }

// Order reports whether the field holds a list's final key order.
func (s FieldSource) Order() bool { return s.order }

// Index reports whether the field is an indexed edit's base index.
func (s FieldSource) Index() bool { return s.index }

// Encode returns the option as the bytes of field 52000 of FieldOptions.
func (s FieldSource) Encode() []byte {
	var b []byte
	if s.source != "" {
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendString(b, string(s.source))
	}
	for i, v := range []bool{s.key, s.order, s.index} {
		if v {
			b = protowire.AppendTag(b, protowire.Number(i+2), protowire.VarintType)
			b = protowire.AppendVarint(b, 1)
		}
	}
	return wrap(b)
}

// EncodeChange returns a change option as the bytes of field 52000 of
// MessageOptions.
func EncodeChange(source protoreflect.FullName, kind Kind) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, string(source))
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(kind))
	return wrap(b)
}

func wrap(payload []byte) []byte {
	b := protowire.AppendTag(nil, ExtensionNumber, protowire.BytesType)
	return protowire.AppendBytes(b, payload)
}

// MessageChange returns the change option of a generated message.
func MessageChange(md protoreflect.MessageDescriptor) (protoreflect.FullName, Kind, bool) {
	b, ok := optionBytes(md.Options())
	if !ok {
		return "", 0, false
	}
	var source protoreflect.FullName
	var kind Kind
	err := scan(b, func(num protowire.Number, typ protowire.Type, v []byte, n uint64) {
		switch {
		case num == 1 && typ == protowire.BytesType:
			source = protoreflect.FullName(v)
		case num == 2 && typ == protowire.VarintType:
			kind = Kind(int32(n))
		}
	})
	if err != nil {
		return "", 0, false
	}
	return source, kind, true
}

// FieldSourceOf returns the field option of a generated field.
func FieldSourceOf(fd protoreflect.FieldDescriptor) (FieldSource, bool) {
	b, ok := optionBytes(fd.Options())
	if !ok {
		return FieldSource{}, false
	}
	var s FieldSource
	err := scan(b, func(num protowire.Number, typ protowire.Type, v []byte, n uint64) {
		switch {
		case num == 1 && typ == protowire.BytesType:
			s.source = protoreflect.FullName(v)
		case num == 2 && typ == protowire.VarintType:
			s.key = n != 0
		case num == 3 && typ == protowire.VarintType:
			s.order = n != 0
		case num == 4 && typ == protowire.VarintType:
			s.index = n != 0
		}
	})
	if err != nil {
		return FieldSource{}, false
	}
	return s, true
}

// optionBytes returns the payload of field 52000 of an options message,
// whether the extension is linked in as a type or kept as unknown fields.
func optionBytes(opts proto.Message) ([]byte, bool) {
	if opts == nil || !opts.ProtoReflect().IsValid() {
		return nil, false
	}
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(opts)
	if err != nil {
		return nil, false
	}
	var out []byte
	found := false
	err = scan(b, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) {
		if num == ExtensionNumber && typ == protowire.BytesType {
			// Repeated occurrences of a message field merge, which for these
			// messages is the same as concatenating them.
			out = append(out, v...)
			found = true
		}
	})
	return out, found && err == nil
}

func scan(b []byte, f func(num protowire.Number, typ protowire.Type, v []byte, n uint64)) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		switch typ {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			f(num, typ, nil, v)
			b = b[n:]
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			f(num, typ, v, 0)
			b = b[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			b = b[n:]
		}
	}
	return nil
}
