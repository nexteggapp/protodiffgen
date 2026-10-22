package generate

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/nexteggapp/protodiffgen/internal/schema"
)

// config is a parsed and checked DiffConfig.
type config struct {
	target       protoreflect.MessageDescriptor
	pkg          protoreflect.FullName
	fileOptions  protoreflect.Message // nil when unset
	atomicTypes  map[protoreflect.FullName]bool
	atomicFields map[protoreflect.FullName]bool
	rules        map[protoreflect.FullName]*rule
	clearName    string
}

type rule struct {
	where    string // such as `lists[2] (example.v1.Team.members)`
	field    protoreflect.FieldDescriptor
	strategy int32
	key      protoreflect.FieldDescriptor // KEYED only
	ordered  bool
}

// parseConfig reads a DiffConfig in text format and checks it against files.
func parseConfig(text []byte, files *protoregistry.Files) (*config, []error) {
	msg := dynamicpb.NewMessage(schema.DiffConfig())
	opts := prototext.UnmarshalOptions{Resolver: dynamicpb.NewTypes(files)}
	if err := opts.Unmarshal(text, msg); err != nil {
		return nil, []error{fmt.Errorf("config: %v", err)}
	}
	md := msg.Descriptor()
	get := func(name protoreflect.Name) protoreflect.Value { return msg.Get(md.Fields().ByName(name)) }
	strs := func(name protoreflect.Name) []string {
		l := get(name).List()
		out := make([]string, l.Len())
		for i := range out {
			out[i] = l.Get(i).String()
		}
		return out
	}

	var errs []error
	errorf := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("config: "+format, args...))
	}
	c := &config{
		atomicTypes:  map[protoreflect.FullName]bool{},
		atomicFields: map[protoreflect.FullName]bool{},
		rules:        map[protoreflect.FullName]*rule{},
	}

	target := get("target_message").String()
	switch d, err := files.FindDescriptorByName(protoreflect.FullName(target)); {
	case target == "":
		errorf("target_message is not set")
	case err != nil:
		errorf("target_message: message %q doesn't exist", target)
	default:
		m, ok := d.(protoreflect.MessageDescriptor)
		if !ok {
			errorf("target_message: %q is not a message", target)
		} else {
			c.target = m
		}
	}

	pkg := get("output_package").String()
	if pkg == "" {
		errorf("output_package is not set")
	} else if !protoreflect.FullName(pkg).IsValid() {
		errorf("output_package: %q is not a valid package name", pkg)
	}
	c.pkg = protoreflect.FullName(pkg)

	if fo := md.Fields().ByName("file_options"); msg.Has(fo) {
		c.fileOptions = msg.Get(fo).Message()
		c.fileOptions.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
			switch fd.Name() {
			case "features":
				errorf("file_options.features: not supported; each generated field sets its own features")
			case "uninterpreted_option":
				errorf("file_options.uninterpreted_option: not supported")
			}
			return true
		})
	}

	for i, name := range strs("atomic_types") {
		d, err := files.FindDescriptorByName(protoreflect.FullName(name))
		if err != nil {
			errorf("atomic_types[%d]: message %q doesn't exist", i, name)
			continue
		}
		if _, ok := d.(protoreflect.MessageDescriptor); !ok {
			errorf("atomic_types[%d]: %q is not a message", i, name)
			continue
		}
		c.atomicTypes[d.FullName()] = true
	}

	for i, name := range strs("atomic_fields") {
		fd := findField(files, name)
		if fd == nil {
			errorf("atomic_fields[%d]: field %q doesn't exist", i, name)
			continue
		}
		if fd.Cardinality() == protoreflect.Repeated {
			errorf("atomic_fields[%d] (%s): the field is repeated; give it a REPLACE list rule instead", i, name)
			continue
		}
		c.atomicFields[fd.FullName()] = true
	}

	lists := get("lists").List()
	for i := 0; i < lists.Len(); i++ {
		if r, err := parseRule(i, lists.Get(i).Message(), files); err != nil {
			errs = append(errs, err)
		} else if prev, ok := c.rules[r.field.FullName()]; ok {
			errorf("%s: a second rule for the field; the first is %s", r.where, strings.SplitN(prev.where, " ", 2)[0])
		} else {
			c.rules[r.field.FullName()] = r
		}
	}

	c.clearName = get("clear_field_name").String()
	if c.clearName == "" {
		c.clearName = "clear"
	} else if !isIdent(c.clearName) {
		errorf("clear_field_name: %q is not a valid field name", c.clearName)
	}
	return c, errs
}

func parseRule(i int, m protoreflect.Message, files *protoregistry.Files) (*rule, error) {
	md := m.Descriptor()
	get := func(name protoreflect.Name) protoreflect.Value { return m.Get(md.Fields().ByName(name)) }
	name := get("field").String()
	where := fmt.Sprintf("lists[%d] (%s)", i, name)
	fail := func(format string, args ...any) (*rule, error) {
		return nil, fmt.Errorf("config: %s: "+format, append([]any{where}, args...)...)
	}
	if name == "" {
		return fail("field is not set")
	}
	fd := findField(files, name)
	if fd == nil {
		return fail("field %q doesn't exist", name)
	}
	if fd.IsMap() {
		return fail("a map field is keyed by its map key and takes no rule")
	}
	if !fd.IsList() {
		return fail("the field is not repeated")
	}
	r := &rule{where: where, field: fd, strategy: int32(get("strategy").Enum())}
	key := get("key").String()
	order := int32(get("order").Enum())
	switch r.strategy {
	case schema.StrategyKeyed:
		item := fd.Message()
		if item == nil {
			return fail("KEYED needs a list of messages; a repeated scalar can be INDEXED or REPLACE")
		}
		if key == "" {
			return fail("KEYED rule has no key")
		}
		kf := item.Fields().ByName(protoreflect.Name(key))
		if kf == nil {
			return fail("key %q is not a field of %s", key, item.FullName())
		}
		if kf.Cardinality() == protoreflect.Repeated || !isKeyKind(kf.Kind()) {
			return fail("key %s is not a singular string, integer, bool or enum field", kf.FullName())
		}
		r.key = kf
		switch order {
		case schema.OrderOrdered:
			r.ordered = true
		case schema.OrderUnordered:
		default:
			return fail("KEYED rule has no order; set ORDERED or UNORDERED")
		}
	case schema.StrategyIndexed, schema.StrategyReplace:
		if key != "" {
			return fail("key is set, but only a KEYED rule takes a key")
		}
		if order != schema.OrderUnspecified {
			return fail("order is set, but only a KEYED rule takes an order")
		}
	default:
		return fail("strategy is not set")
	}
	return r, nil
}

func findField(files *protoregistry.Files, name string) protoreflect.FieldDescriptor {
	d, err := files.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		return nil
	}
	fd, ok := d.(protoreflect.FieldDescriptor)
	if !ok || fd.IsExtension() {
		return nil
	}
	return fd
}

func isKeyKind(k protoreflect.Kind) bool {
	switch k {
	case protoreflect.StringKind, protoreflect.BoolKind, protoreflect.EnumKind,
		protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.Sint32Kind, protoreflect.Sint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind,
		protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		return true
	}
	return false
}

func isIdent(s string) bool {
	return s != "" && protoreflect.Name(s).IsValid()
}
