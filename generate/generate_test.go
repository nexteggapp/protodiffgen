package generate_test

import (
	"flag"
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"

	"github.com/nexteggapp/protodiffgen/generate"
	"github.com/nexteggapp/protodiffgen/internal/schema"
	"github.com/nexteggapp/protodiffgen/internal/testschema"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func checkGolden(t *testing.T, got []byte, path string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if want := read(t, path); string(got) != want {
		t.Errorf("%s differs; run go test ./generate -update to see how.\ngot:\n%s", path, got)
	}
}

// TestGoldenTeam checks the output for the sample config in docs/design.md.
// Build also compiles the output, so it must be a valid .proto.
func TestGoldenTeam(t *testing.T) {
	s, err := testschema.Build(map[string]string{
		"example/v1/team.proto": read(t, "../examples/example/v1/team.proto"),
	}, []byte(read(t, "../examples/team.diff.textproto")))
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, s.Text(), "testdata/team_diff.proto.golden")
}

// TestGoldenTestSchema checks the output for the test schema, which reaches
// every shape.
func TestGoldenTestSchema(t *testing.T) {
	s, err := testschema.Load()
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, s.Text(), "testdata/pdgtest_diff.proto.golden")
}

// TestConfigDescriptor checks the descriptor built in Go against
// v1/config.proto.
func TestConfigDescriptor(t *testing.T) {
	files, err := testschema.Compile(map[string]string{schema.Path: read(t, "../v1/config.proto")})
	if err != nil {
		t.Fatal(err)
	}
	got := protodesc.ToFileDescriptorProto(files[0])
	got.SourceCodeInfo = nil
	if want := schema.FileProto(); !proto.Equal(got, want) {
		t.Errorf("config.proto and internal/schema differ\nconfig.proto: %v\nschema: %v", got, want)
	}
}

const team = `
syntax = "proto3";
package example.v1;
message Team {
  string name = 1;
  Money budget = 2;
  repeated Member members = 3;
  repeated string tags = 4;
  map<string, Member> by_name = 5;
}
message Member {
  string id = 1;
  repeated Member mentees = 2;
  double score = 3;
  Money pay = 4;
  repeated string ids = 5;
}
message Money { int64 units = 1; }
`

const baseConfig = `
target_message: "example.v1.Team"
output_package: "example.v1.diff"
lists { field: "example.v1.Team.members" strategy: KEYED key: "id" order: ORDERED }
lists { field: "example.v1.Member.mentees" strategy: REPLACE }
`

func generateFrom(t *testing.T, sources map[string]string, config string) ([]byte, error) {
	t.Helper()
	s, err := testschema.Build(sources, []byte(config))
	if err != nil {
		return nil, err
	}
	return s.Text(), nil
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name    string
		sources map[string]string
		config  string
		want    string
	}{
		{"target unset", nil, `output_package: "x"`, "target_message is not set"},
		{"target missing", nil, `target_message: "example.v1.Nope" output_package: "x"`, `target_message: message "example.v1.Nope" doesn't exist`},
		{"target not a message", nil, `target_message: "example.v1.Team.name" output_package: "x"`, `target_message: "example.v1.Team.name" is not a message`},
		{"target atomic", nil, `target_message: "google.protobuf.Empty" output_package: "x"`, "target_message: google.protobuf.Empty is atomic"},
		{"package unset", nil, `target_message: "example.v1.Team"`, "output_package is not set"},
		{"package invalid", nil, `target_message: "example.v1.Team" output_package: "a..b"`, `output_package: "a..b" is not a valid package name`},
		{"atomic type missing", nil, baseConfig + `atomic_types: "example.v1.Cash"`, `atomic_types[0]: message "example.v1.Cash" doesn't exist`},
		{"atomic type not a message", nil, baseConfig + `atomic_types: "example.v1.Team.name"`, `atomic_types[0]: "example.v1.Team.name" is not a message`},
		{"atomic field missing", nil, baseConfig + `atomic_fields: "example.v1.Team.cash"`, `atomic_fields[0]: field "example.v1.Team.cash" doesn't exist`},
		{"atomic field repeated", nil, baseConfig + `atomic_fields: "example.v1.Team.tags"`, "atomic_fields[0] (example.v1.Team.tags): the field is repeated; give it a REPLACE list rule instead"},
		{"rule field unset", nil, baseConfig + `lists { strategy: REPLACE }`, "lists[2] (): field is not set"},
		{"rule field missing", nil, baseConfig + `lists { field: "example.v1.Team.nope" strategy: REPLACE }`, `lists[2] (example.v1.Team.nope): field "example.v1.Team.nope" doesn't exist`},
		{"rule on a map", nil, baseConfig + `lists { field: "example.v1.Team.by_name" strategy: REPLACE }`, "lists[2] (example.v1.Team.by_name): a map field is keyed by its map key and takes no rule"},
		{"rule on a singular field", nil, baseConfig + `lists { field: "example.v1.Team.name" strategy: REPLACE }`, "lists[2] (example.v1.Team.name): the field is not repeated"},
		{"rule with no strategy", nil, baseConfig + `lists { field: "example.v1.Team.tags" }`, "lists[2] (example.v1.Team.tags): strategy is not set"},
		{"keyed with no key", nil, `target_message: "example.v1.Team" output_package: "x" lists { field: "example.v1.Team.members" strategy: KEYED order: ORDERED }`, "lists[0] (example.v1.Team.members): KEYED rule has no key"},
		{"keyed with a missing key", nil, `target_message: "example.v1.Team" output_package: "x" lists { field: "example.v1.Team.members" strategy: KEYED key: "nope" order: ORDERED }`, `lists[0] (example.v1.Team.members): key "nope" is not a field of example.v1.Member`},
		{"keyed by a float", nil, `target_message: "example.v1.Team" output_package: "x" lists { field: "example.v1.Team.members" strategy: KEYED key: "score" order: ORDERED }`, "key example.v1.Member.score is not a singular string, integer, bool or enum field"},
		{"keyed by a message", nil, `target_message: "example.v1.Team" output_package: "x" lists { field: "example.v1.Team.members" strategy: KEYED key: "pay" order: ORDERED }`, "key example.v1.Member.pay is not a singular string, integer, bool or enum field"},
		{"keyed by a repeated field", nil, `target_message: "example.v1.Team" output_package: "x" lists { field: "example.v1.Team.members" strategy: KEYED key: "ids" order: ORDERED }`, "key example.v1.Member.ids is not a singular string, integer, bool or enum field"},
		{"keyed with no order", nil, `target_message: "example.v1.Team" output_package: "x" lists { field: "example.v1.Team.members" strategy: KEYED key: "id" }`, "lists[0] (example.v1.Team.members): KEYED rule has no order; set ORDERED or UNORDERED"},
		{"keyed scalars", nil, baseConfig + `lists { field: "example.v1.Team.tags" strategy: KEYED key: "x" order: ORDERED }`, "lists[2] (example.v1.Team.tags): KEYED needs a list of messages"},
		{"indexed with a key", nil, baseConfig + `lists { field: "example.v1.Team.tags" strategy: INDEXED key: "id" }`, "lists[2] (example.v1.Team.tags): key is set, but only a KEYED rule takes a key"},
		{"replace with an order", nil, baseConfig + `lists { field: "example.v1.Team.tags" strategy: REPLACE order: ORDERED }`, "lists[2] (example.v1.Team.tags): order is set, but only a KEYED rule takes an order"},
		{"two rules for one field", nil, baseConfig + `lists { field: "example.v1.Team.members" strategy: REPLACE }`, "lists[2] (example.v1.Team.members): a second rule for the field; the first is lists[0]"},
		{"a repeated message with no rule", nil, `target_message: "example.v1.Team" output_package: "x" lists { field: "example.v1.Team.members" strategy: KEYED key: "id" order: ORDERED }`, "example.v1.Member.mentees: repeated message field has no list rule"},
		{"bad clear name", nil, baseConfig + `clear_field_name: "9x"`, `clear_field_name: "9x" is not a valid field name`},
		{"file features", nil, baseConfig + `file_options { features { field_presence: IMPLICIT } }`, "file_options.features: not supported"},
		{"bad config", nil, `target: "x"`, `config: `},
		{
			"two patch bodies with one name",
			map[string]string{"a.proto": `syntax = "proto3"; package p;
				message T { Sale.Holding h = 1; Sale_Holding g = 2; }
				message Sale { message Holding { int32 n = 1; } }
				message Sale_Holding { int32 n = 1; }`},
			`target_message: "p.T" output_package: "p.diff"`,
			"p.Sale.Holding and p.Sale_Holding both get the patch body name Sale_HoldingDiff",
		},
		{
			"a patch body that collides with a source type",
			map[string]string{"a.proto": `syntax = "proto3"; package p; message T { int32 n = 1; } message TDiff {}`},
			`target_message: "p.T" output_package: "p"`,
			"p.T: its patch body p.TDiff collides with an existing p.TDiff",
		},
		{
			"a source field named like the clear field",
			map[string]string{"a.proto": `syntax = "proto3"; package p; message T { int32 clear = 1; }`},
			`target_message: "p.T" output_package: "p.diff"`,
			`field p.T.clear and the clear field both use the name "clear"`,
		},
		{
			"a source field named like an order field",
			map[string]string{"a.proto": `syntax = "proto3"; package p; message T { repeated I items = 1; int32 items_order = 2; } message I { string id = 1; }`},
			`target_message: "p.T" output_package: "p.diff" lists { field: "p.T.items" strategy: KEYED key: "id" order: ORDERED }`,
			`field p.T.items_order and the order field of p.T.items both use the name "items_order"`,
		},
		{
			"a source field with an order field's JSON name",
			map[string]string{"a.proto": `syntax = "proto3"; package p; message T { repeated I items = 1; int32 itemsOrder = 2; } message I { string id = 1; }`},
			`target_message: "p.T" output_package: "p.diff" lists { field: "p.T.items" strategy: KEYED key: "id" order: ORDERED }`,
			`field p.T.itemsOrder and the order field of p.T.items both have the JSON name "itemsOrder"`,
		},
		{
			"two generated messages with one name",
			map[string]string{"a.proto": `syntax = "proto2"; package p; message T { repeated int32 foo_bar = 1; repeated int32 fooBar = 2; }`},
			`target_message: "p.T" output_package: "p.diff"`,
			`the message FooBarReplace made for p.T.foo_bar and the message FooBarReplace made for p.T.fooBar both use the name "FooBarReplace"`,
		},
		{
			"a key named like an op",
			map[string]string{"a.proto": `syntax = "proto3"; package p; message T { repeated I items = 1; } message I { string patch = 1; }`},
			`target_message: "p.T" output_package: "p.diff" lists { field: "p.T.items" strategy: KEYED key: "patch" order: UNORDERED }`,
			`the key field p.I.patch and the patch field both use the name "patch"`,
		},
		{
			"a field in the clear field's number",
			map[string]string{"a.proto": `syntax = "proto3"; package p; message T { int32 n = 536870911; }`},
			`target_message: "p.T" output_package: "p.diff"`,
			"p.T.n: number 536870911 is in the range generated fields use in TDiff (536870911 and up)",
		},
		{
			"a field in an order field's number",
			map[string]string{"a.proto": `syntax = "proto3"; package p; message T { repeated I items = 1; int32 n = 536870910; } message I { string id = 1; }`},
			`target_message: "p.T" output_package: "p.diff" lists { field: "p.T.items" strategy: KEYED key: "id" order: ORDERED }`,
			"p.T.n: number 536870910 is in the range generated fields use in TDiff (536870910 and up)",
		},
		{
			"a group",
			map[string]string{"a.proto": `syntax = "proto2"; package p; message T { optional group G = 1 { optional int32 n = 2; } }`},
			`target_message: "p.T" output_package: "p.diff"`,
			"p.T.g: groups are not supported",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sources := tt.sources
			if sources == nil {
				sources = map[string]string{"example/v1/team.proto": team}
			}
			_, err := generateFrom(t, sources, tt.config)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestReportsEveryError(t *testing.T) {
	_, err := generateFrom(t, map[string]string{"example/v1/team.proto": team},
		`target_message: "example.v1.Team" output_package: "x" atomic_types: "a.B" atomic_fields: "a.B.c"`)
	if err == nil || !strings.Contains(err.Error(), "atomic_types[0]") || !strings.Contains(err.Error(), "atomic_fields[0]") {
		t.Fatalf("got %v", err)
	}
}

// TestNames checks that a type that something in scope would shadow is
// written with a leading dot, and that file options from extensions are
// written and imported.
func TestNames(t *testing.T) {
	sources := map[string]string{
		"example/v1/team.proto": `syntax = "proto3"; package example.v1; message Team { Money m = 1; } message Money { int64 units = 1; }`,
		"opts/opts.proto": `syntax = "proto3"; package opts; import "google/protobuf/descriptor.proto";
			extend google.protobuf.FileOptions { string tag = 50001; Meta meta = 50002; }
			message Meta { string a = 1; repeated int32 b = 2; }`,
	}
	got, err := generateFrom(t, sources, `
		target_message: "example.v1.Team"
		output_package: "foo.example"
		atomic_types: "example.v1.Money"
		file_options { java_package: "x\"y" [opts.tag]: "t" [opts.meta] { a: "z" b: 1 b: 2 } }`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`.example.v1.Money m = 1`,
		`import "opts/opts.proto";`,
		`option java_package = "x\"y";`,
		`option (opts.tag) = "t";`,
		`option (opts.meta) = { a: "z" b: 1 b: 2 };`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("output lacks %s:\n%s", want, got)
		}
	}
}

func TestCLIErrorsAreClear(t *testing.T) {
	if _, err := generate.File(nil, []byte(`target_message: "a.B" output_package: "c"`)); err == nil || !strings.Contains(err.Error(), `message "a.B" doesn't exist`) {
		t.Fatalf("got %v", err)
	}
}
