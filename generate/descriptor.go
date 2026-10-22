package generate

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/nexteggapp/protodiffgen/internal/schema"
)

// descriptor returns the generated file as a FileDescriptorProto, with the
// protodiffgen options kept as unknown fields.
func (g *gen) descriptor() *descriptorpb.FileDescriptorProto {
	fd := &descriptorpb.FileDescriptorProto{
		Name:       proto.String(string(g.cfg.pkg) + ".diff.proto"),
		Package:    proto.String(string(g.cfg.pkg)),
		Dependency: sortedKeys(g.imports),
		Syntax:     proto.String("editions"),
		Edition:    descriptorpb.Edition_EDITION_2023.Enum(),
	}
	if g.cfg.fileOptions != nil {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(g.cfg.fileOptions.Interface())
		if err == nil {
			fd.Options = &descriptorpb.FileOptions{}
			if err := proto.Unmarshal(b, fd.Options); err != nil {
				fd.Options = nil
			}
		}
	}
	for _, m := range g.top {
		fd.MessageType = append(fd.MessageType, m.descriptor())
	}
	return fd
}

func (m *message) descriptor() *descriptorpb.DescriptorProto {
	d := &descriptorpb.DescriptorProto{Name: proto.String(m.name)}
	if m.kind != schema.KindUnspecified {
		d.Options = &descriptorpb.MessageOptions{}
		d.Options.ProtoReflect().SetUnknown(schema.EncodeChange(m.source, m.kind))
	}
	for _, o := range m.oneofs {
		d.OneofDecl = append(d.OneofDecl, &descriptorpb.OneofDescriptorProto{Name: proto.String(o)})
	}
	for _, f := range m.ordered() {
		d.Field = append(d.Field, f.descriptor())
	}
	for _, n := range m.nested {
		d.NestedType = append(d.NestedType, n.descriptor())
	}
	return d
}

// ordered returns the fields in the order they are printed: each oneof's
// fields together, where its first field is.
func (m *message) ordered() []*field {
	var out []*field
	done := map[int]bool{}
	for _, f := range m.fields {
		if f.oneof < 0 {
			out = append(out, f)
			continue
		}
		if done[f.oneof] {
			continue
		}
		done[f.oneof] = true
		for _, g := range m.fields {
			if g.oneof == f.oneof {
				out = append(out, g)
			}
		}
	}
	return out
}

func (f *field) descriptor() *descriptorpb.FieldDescriptorProto {
	d := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(f.name),
		Number:   proto.Int32(int32(f.number)),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_Type(f.kind).Enum(),
		JsonName: proto.String(schema.JSONName(f.name)),
	}
	if f.repeated {
		d.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	}
	if f.ref != "" {
		d.TypeName = proto.String("." + string(f.ref))
	}
	if f.oneof >= 0 {
		d.OneofIndex = proto.Int32(int32(f.oneof))
	}
	if f.source != nil || len(f.features) > 0 {
		d.Options = &descriptorpb.FieldOptions{}
		for _, ft := range f.features {
			if d.Options.Features == nil {
				d.Options.Features = &descriptorpb.FeatureSet{}
			}
			switch ft.name {
			case "utf8_validation":
				d.Options.Features.Utf8Validation = descriptorpb.FeatureSet_NONE.Enum()
			case "repeated_field_encoding":
				d.Options.Features.RepeatedFieldEncoding = descriptorpb.FeatureSet_EXPANDED.Enum()
			}
		}
		if f.source != nil {
			d.Options.ProtoReflect().SetUnknown(f.source.Encode())
		}
	}
	return d
}
