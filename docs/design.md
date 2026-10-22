# protodiffgen design

> **Status: designed, not built.**

## What it is

Many systems keep a protobuf message and need to describe a change to it: an
edit to send over the wire, a variation stored beside a base, a draft to
compare with what was saved. protobuf has no type for that. `FieldMask` names
fields but can't say "patch the list item whose id is `ada`", and merging two
messages appends to every repeated field.

protodiffgen fills the gap. For a target message it generates diff messages,
one per message type the target reaches, from a config that says how each list
is matched. A Go runtime computes and applies them. A tax-planning app, for
example, keeps one year's inputs and stores each "what if" scenario as a diff
of them: "a raise from October" is a patch to one employer, keyed by its id.

Terms used below:

- **Target**: the message being diffed, such as `example.v1.Team`.
- **Keyed list**: a repeated message field whose items are matched by one of
  their own fields, the **key**, such as a member's `id`.
- **Atomic**: a field or type that is always set whole, never patched, such as
  a money amount or a date.

## Goals and non-goals

**Goals**

- A diff is a typed protobuf message, so it can be stored, sent and read with
  ordinary tools.
- A list item is named by its key, never its index, so two diffs made from one
  base can be compared item by item.
- The target's `.proto` files stay unaware of protodiffgen.
- A mistake in the config fails the build, not a later `Apply`.

**Non-goals**

- Text diffs, and diffs between two different schemas.
- Three-way merge, and any policy for resolving two diffs that touch one item.
  `Overlap` reports them; the caller decides.
- Runtimes in languages other than Go.

## The config

The config is a proto in protodiffgen, written as textproto, and kept outside
the target's files.

```proto
// nextgui.xyz/protodiffgen/v1/config.proto
syntax = "proto3";
package nextgui.protodiffgen.v1;

import "google/protobuf/descriptor.proto";

option go_package = "github.com/nexteggapp/protodiffgen/v1;protodiffgenv1";

message DiffConfig {
  // The full name of the target, such as "example.v1.Team".
  string target_message = 1;
  // The package the generated messages go in.
  string output_package = 2;
  // Message types always set whole, wherever they appear.
  repeated string atomic_types = 3;
  // Fields, by full name, always set whole.
  repeated string atomic_fields = 4;
  // One rule per repeated message field the target reaches.
  repeated ListRule lists = 5;
}

message ListRule {
  enum Strategy {
    STRATEGY_UNSPECIFIED = 0;
    // Items are matched by `key`.
    KEYED = 1;
    // The list is set whole.
    REPLACE = 2;
  }
  // The repeated field's full name, such as "example.v1.Team.members".
  string field = 1;
  Strategy strategy = 2;
  // For KEYED: a singular scalar or enum field of the item.
  string key = 3;
}

// Options on generated messages. The numbers are placeholders until
// registered (see "Open questions").
message Change {
  enum Kind {
    KIND_UNSPECIFIED = 0;
    PATCH = 1;
    LIST_CHANGE = 2;
    REPLACE = 3;
  }
  // The full name of the message or field this diffs.
  string source = 1;
  Kind kind = 2;
}

extend google.protobuf.MessageOptions {
  Change change = 52000;
}

extend google.protobuf.FieldOptions {
  string source_field = 52001;
  // On a list change's key field: the item's key field.
  string key = 52002;
}
```

The build fails when the config:

- names a message or field that doesn't exist;
- leaves a repeated message field the target reaches without a list rule;
- gives a key that isn't a singular scalar or enum field of the item.

Two kinds of field need no rule. A repeated scalar is always replaced whole. A
map field is keyed by its map key.

## What is generated

For this target and config:

```proto
package example.v1;
message Team {
  string name = 1;
  Money budget = 2;
  repeated Member members = 3;
  repeated string tags = 4;
}
message Member {
  string id = 1;
  string name = 2;
  Role role = 3;
  repeated Address addresses = 4;
}
```

```textproto
target_message: "example.v1.Team"
output_package: "example.v1.diff"
atomic_types: "example.v1.Money"
lists { field: "example.v1.Team.members" strategy: KEYED key: "id" }
lists { field: "example.v1.Member.addresses" strategy: REPLACE }
```

protodiffgen writes:

```proto
package example.v1.diff;

import "example/v1/team.proto";
import "google/protobuf/empty.proto";
import "nextgui.xyz/protodiffgen/v1/config.proto";

message TeamDiff {
  option (nextgui.protodiffgen.v1.change) = { source: "example.v1.Team" kind: PATCH };

  optional string name = 1 [(nextgui.protodiffgen.v1.source_field) = "example.v1.Team.name"];
  // Atomic: present means "set to this".
  example.v1.Money budget = 2 [(nextgui.protodiffgen.v1.source_field) = "example.v1.Team.budget"];
  repeated MembersChange members = 3 [(nextgui.protodiffgen.v1.source_field) = "example.v1.Team.members"];
  TagsReplace tags = 4 [(nextgui.protodiffgen.v1.source_field) = "example.v1.Team.tags"];
  // Fields with presence that become unset, by name.
  repeated string clear = 536870911;

  message MembersChange {
    option (nextgui.protodiffgen.v1.change) = { source: "example.v1.Team.members" kind: LIST_CHANGE };
    string id = 1 [(nextgui.protodiffgen.v1.key) = "id"];
    oneof op {
      example.v1.Member add = 2;
      google.protobuf.Empty remove = 3;
      MemberDiff patch = 4;
    }
  }

  // Present means "use this list".
  message TagsReplace {
    option (nextgui.protodiffgen.v1.change) = { source: "example.v1.Team.tags" kind: REPLACE };
    repeated string tags = 1;
  }
}

message MemberDiff {
  option (nextgui.protodiffgen.v1.change) = { source: "example.v1.Member" kind: PATCH };
  // No field 1: a key is never patched. Changing it is a remove and an add.
  optional string name = 2 [(nextgui.protodiffgen.v1.source_field) = "example.v1.Member.name"];
  optional example.v1.Role role = 3 [(nextgui.protodiffgen.v1.source_field) = "example.v1.Member.role"];
  AddressesReplace addresses = 4 [(nextgui.protodiffgen.v1.source_field) = "example.v1.Member.addresses"];
  repeated string clear = 536870911;

  message AddressesReplace {
    option (nextgui.protodiffgen.v1.change) = { source: "example.v1.Member.addresses" kind: REPLACE };
    repeated example.v1.Address addresses = 1;
  }
}
```

The rules:

- **One patch body per type**, `FooDiff`, for every message type the target
  reaches that isn't atomic.
- **Every scalar is `optional`.** Absent means unchanged.
- **An atomic message is set whole**, and a non-atomic one is diffed, its field
  typed `FooDiff`. Patching an unset message starts it from empty.
- **Unsetting** a field with presence is done by naming it in `clear`, the one
  field the source doesn't have. It takes the highest field number protobuf
  allows, so it never collides.
- **A oneof** keeps its cases, each typed as above. A diff that sets a different
  case from the one the target has replaces the oneof whole: `Apply` starts the
  new case from empty.
- **A list change** is a wrapper nested in the parent's diff and named after the
  field, such as `TeamDiff.MembersChange`. It holds the key, in the key field's
  type, and one of `add`, `remove` or `patch`. A map with scalar values has
  `set` and `remove` instead.
- **A REPLACE list** is a wrapper, such as `TagsReplace`, whose presence means
  "use this list".
- **Field numbers match the source**, so a diff field and its source field are
  easy to line up.
- **Options record the source.** `change` says what a generated message diffs
  and how, `source_field` names each field's source and `key` marks a key.
  With them the runtime needs only the diff's descriptor, never the config.

A keyed list is treated as a set. Its order carries no meaning: `Apply` puts
an added item at the end and leaves a patched one where it is, and two lists
with the same items in another order are equal.

## Using it

**Bazel.** The module provides `proto_diff_library`:

```starlark
load("@protodiffgen//:defs.bzl", "proto_diff_library")

proto_diff_library(
    name = "team_diff_proto",
    config = "team.diff.textproto",
    deps = [":team_proto"],
)
```

It runs the CLI over the `proto_library`'s descriptor set and produces a
`proto_library` named `team_diff_proto`, from which any language's bindings
are generated as usual. The `.proto` is built, never checked in.

**CLI.** For buf or protoc users:

```bash
buf build -o team.binpb            # or: protoc --include_imports --descriptor_set_out=team.binpb ...
protodiffgen --descriptor_set=team.binpb --config=team.diff.textproto --out=team_diff.proto
```

**Go.** Package `apply` works by reflection, so it needs no generated Go of its
own. The caller's ordinary generated types are passed in.

```go
// Diff writes into out, an empty diff message, the smallest diff from a to b.
func Diff(a, b, out proto.Message) error

// Apply returns a with d applied. a is not changed.
func Apply(a, d proto.Message) (proto.Message, error)

// Overlap lists the keyed items both diffs touch, at any depth. Fields
// outside every keyed item count as one item, the root.
func Overlap(d1, d2 proto.Message) ([]Item, error)

// Validate checks d against a: every remove and patch names a key a has, and
// every add names a key it doesn't.
func Validate(a, d proto.Message) error

// Item is one keyed item: its path from the root, such as members[ada].
type Item struct{ Path []Step }
type Step struct {
	Field protoreflect.FullName
	Key   protoreflect.Value
}
```

A list with two items of one key is refused by `Diff`, `Apply` and `Validate`.

## Tests

- **Property tests** over random messages of a test schema with every shape
  above: `Apply(a, Diff(a, b))` equals `b`, and `Diff(a, Apply(a, d))` equals
  `d` for a normalised `d`. A diff is normalised when it has no entry that
  changes nothing and its list changes are in key order. `Diff` only produces
  normalised diffs.
- **Golden tests** of the generated `.proto` for the sample config above.

## CI

GitHub Actions runs `bazel test //...` and `go test ./...` on every pull
request, with Bazel 9.2.0 and Go 1.25.

## Prior art

`FieldMask` selects fields and merges by replacing them, with no way to name a
list item; Google's AIP-161 lets a mask name a map key, but a repeated field
can only be appended to. JSON Patch addresses list items by index, which shifts
when another change inserts. JSON Merge Patch replaces arrays whole.
Kubernetes' strategic merge patch has merge keys and is the closest match, but
it works on Go structs and JSON, not on protobuf schemas. For protobuf, the C++
`MessageDifferencer` can compare a repeated field as a map by key, and Go's
`protocmp` can sort repeated fields before comparing, but both report
differences for a reader; neither produces a diff you can store and apply. I
found no maintained protobuf library that does keyed list diffs.

## Publishing

At first the module is used pinned to a git commit: Bazel's `git_override`,
and `go get github.com/nexteggapp/protodiffgen@<commit>`. After a v0.1 tag it
goes to the Bazel Central Registry.

## Open questions

- **Extension numbers.** The options' numbers are placeholders. They should be
  registered in protobuf's global extension registry before v0.1.
- **List order.** A keyed list that means something in order, such as steps,
  can't be expressed. A `move` operation could be added if one is needed.
