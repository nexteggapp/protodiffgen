# protodiffgen design

> **Status: v0.1 is built, not yet tagged.** The extension number is still a
> placeholder; see Open questions.

## What it is

Many systems keep a protobuf message and need to describe a change to it: an
edit to send over the wire, a variation stored beside a base, a draft to
compare with what was saved. protobuf has no type for that. `FieldMask` names
fields but can't say "patch the list item whose id is `ada`", and merging two
messages appends to every repeated field.

protodiffgen fills the gap. For a target message it generates diff messages,
from a config that says how each list is matched, and a Go runtime computes and
applies them. A tax-planning app, for example, keeps one year's inputs and
stores each "what if" scenario as a diff of them: "a raise from October" is a
patch to one employer, keyed by its id.

Terms used below:

- **Target**: the message being diffed, such as `example.v1.Team`.
- **Keyed list**: a repeated message field whose items are matched by one of
  their own fields, the **key**, such as a member's `id`. Its items are never
  addressed by index.
- **Indexed list**: a list whose edits name positions in the base list.
- **Atomic**: a type or field that is always set whole, never patched, such as
  a money amount or a date.
- **Patch body**: the generated message that describes a change to one message
  type, such as `TeamDiff` for `Team`.
- **Change wrapper**: the generated message for one change to a list: for a
  keyed list the key, and an add, a remove or a patch; for an indexed list a
  base index, and a patch, a remove or an insert.

It has three names, for three namespaces: the proto package
`nextgui.protodiffgen.v1`, the import path
`nextgui.xyz/protodiffgen/v1/config.proto`, and the Go module
`github.com/nexteggapp/protodiffgen`.

## Goals and non-goals

**Goals**

- A diff is a typed protobuf message, so it can be stored, sent and read with
  ordinary tools.
- A keyed list item is named by its key, so two diffs made from one base can be
  compared item by item.
- The target's `.proto` files stay unaware of protodiffgen.
- A mistake in the config fails the build, and a bad diff fails before
  anything is applied.

**Non-goals**

- Text diffs, and diffs between two different schemas.
- Three-way merge, and any policy for two diffs that touch one item. `Overlap`
  reports them; the caller decides.
- Keys made of more than one field.
- Applying a diff to anything but the base it was made from.
- Runtimes in languages other than Go.

## The config

The config is a proto in protodiffgen, written as textproto, kept outside the
target's files.

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
  // Options for the generated file, such as go_package.
  google.protobuf.FileOptions file_options = 3;
  // Message types always set whole, wherever they appear. Every
  // google.protobuf type is atomic without being listed.
  repeated string atomic_types = 4;
  // Singular fields, by full name, always set whole.
  repeated string atomic_fields = 5;
  // One rule per repeated message field the target reaches.
  repeated ListRule lists = 6;
  // The name of the generated field that lists cleared fields. Default "clear".
  string clear_field_name = 7;
}

message ListRule {
  enum Strategy {
    STRATEGY_UNSPECIFIED = 0;
    // Items are matched by `key`.
    KEYED = 1;
    // Edits name positions in the base list.
    INDEXED = 3;
    // The list is set whole.
    REPLACE = 2;
  }
  enum Order {
    ORDER_UNSPECIFIED = 0;
    // The items' order means something, and a diff can change it.
    ORDERED = 1;
    // The list is a set.
    UNORDERED = 2;
  }
  // The repeated field's full name, such as "example.v1.Team.members".
  string field = 1;
  Strategy strategy = 2;
  // For KEYED: a singular string, integer, bool or enum field of the item.
  string key = 3;
  // Required for KEYED.
  Order order = 4;
}

// Where a generated message came from.
message Change {
  enum Kind {
    KIND_UNSPECIFIED = 0;
    PATCH = 1;
    LIST_CHANGE = 2;
    REPLACE = 3;
    LIST_EDIT = 4;
  }
  // The full name of the message or field it diffs.
  string source = 1;
  Kind kind = 2;
}

// Where a generated field came from.
message FieldSource {
  // The full name of the source field.
  string source = 1;
  // This is a change wrapper's key.
  bool key = 2;
  // This holds a list's final key order.
  bool order = 3;
  // This is an indexed edit's base index.
  bool index = 4;
}

// One placeholder number for both, until registered.
extend google.protobuf.MessageOptions {
  Change change = 52000;
}
extend google.protobuf.FieldOptions {
  FieldSource field = 52000;
}
```

The build fails when the config:

- names a message or field that doesn't exist;
- leaves a repeated message field the target reaches without a list rule;
- gives a KEYED rule no `order`, or a key that isn't a singular string,
  integer, bool or enum field of the item, or gives `key` or `order` to an
  INDEXED or REPLACE rule;
- names a repeated field in `atomic_fields`, which a REPLACE rule does instead;
- produces a generated name that collides: two patch bodies with one name, or
  a generated field (`clear`, `<field>_order`) with a source field's name;
- reaches a source field numbered in the range generated fields use (see
  below), or a group.

A repeated scalar is replaced whole unless a rule makes it INDEXED, and a map
field is keyed by its map key, so neither needs a rule. Extension fields are not
part of a diff.

**Choosing a strategy.** Use KEYED when items have an identity of their own: it
is the most robust, because a key still names the same item after other
changes. Use INDEXED for long lists whose items have no identity, where
replacing the whole list for one edit would be wasteful. Use REPLACE for short
ones.

## What is generated

For this target and config:

```proto
syntax = "proto3";
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
file_options { go_package: "example.com/team/v1/diffpb" }
atomic_types: "example.v1.Money"
lists { field: "example.v1.Team.members" strategy: KEYED key: "id" order: ORDERED }
lists { field: "example.v1.Member.addresses" strategy: INDEXED }
```

protodiffgen writes the following, which is checked in as
`examples/team_diff.proto`:

```proto
edition = "2023";

package example.v1.diff;

import "example/v1/team.proto";
import "google/protobuf/empty.proto";
import "nextgui.xyz/protodiffgen/v1/config.proto";

option go_package = "example.com/team/v1/diffpb";

// A change to example.v1.Team. An unset field leaves its value unchanged.
message TeamDiff {
  option (nextgui.protodiffgen.v1.change) = { source: "example.v1.Team" kind: PATCH };

  // Sets example.v1.Team.name when present.
  string name = 1 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Team.name" }];
  // Sets example.v1.Team.budget whole when present: it is atomic.
  example.v1.Money budget = 2 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Team.budget" }];
  // Changes to example.v1.Team.members, one per changed key.
  repeated MembersChange members = 3 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Team.members" }];
  // Sets example.v1.Team.tags whole when present.
  TagsReplace tags = 4 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Team.tags" }];
  // The members' keys in their final order. Present only when it differs
  // from the default: kept items in base order, then added items in key order.
  repeated string members_order = 536870910 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Team.members" order: true }];
  // Numbers of fields that become unset.
  repeated int32 clear = 536870911;

  // A change to one item of example.v1.Team.members, matched by id.
  message MembersChange {
    option (nextgui.protodiffgen.v1.change) = { source: "example.v1.Team.members" kind: LIST_CHANGE };
    // The key of the item this changes.
    string id = 1 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Member.id" key: true }];
    // What the change does.
    oneof op {
      // Adds the item. Its id must equal the key.
      example.v1.Member add = 2;
      // Removes the item.
      google.protobuf.Empty remove = 3;
      // Patches the item. It may not change the key.
      MemberDiff patch = 4;
    }
  }

  // The whole new value of example.v1.Team.tags. Present means "use this list".
  message TagsReplace {
    option (nextgui.protodiffgen.v1.change) = { source: "example.v1.Team.tags" kind: REPLACE };
    // The new list, in order.
    repeated string tags = 1;
  }
}

// A change to example.v1.Member. An unset field leaves its value unchanged.
message MemberDiff {
  option (nextgui.protodiffgen.v1.change) = { source: "example.v1.Member" kind: PATCH };

  // Sets example.v1.Member.id when present.
  // Kept, so MemberDiff serves wherever Member appears. A patch may not change it.
  string id = 1 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Member.id" }];
  // Sets example.v1.Member.name when present.
  string name = 2 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Member.name" }];
  // Sets example.v1.Member.role when present.
  example.v1.Role role = 3 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Member.role" }];
  // Edits to example.v1.Member.addresses, by base index.
  repeated AddressesEdit addresses = 4 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Member.addresses" }];
  // Numbers of fields that become unset.
  repeated int32 clear = 536870911;

  // An edit to example.v1.Member.addresses at one base index.
  message AddressesEdit {
    option (nextgui.protodiffgen.v1.change) = { source: "example.v1.Member.addresses" kind: LIST_EDIT };
    // A position in the base list of n items: [0, n) for patch and remove,
    // [0, n] for insert_before, where n appends.
    uint32 index = 1 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Member.addresses" index: true }];
    // What the edit does.
    oneof op {
      // Patches the element at index.
      AddressDiff patch = 2;
      // Removes the element at index.
      google.protobuf.Empty remove = 3;
      // Inserts elements before the element at index, or at the end when index is n.
      Inserted insert_before = 4;
    }

    // The elements inserted, in order.
    message Inserted {
      // The elements, in order.
      repeated example.v1.Address items = 1;
    }
  }
}

// A change to example.v1.Address. An unset field leaves its value unchanged.
message AddressDiff {
  option (nextgui.protodiffgen.v1.change) = { source: "example.v1.Address" kind: PATCH };

  // Sets example.v1.Address.line when present.
  string line = 1 [(nextgui.protodiffgen.v1.field) = { source: "example.v1.Address.line" }];
  // Numbers of fields that become unset.
  repeated int32 clear = 536870911;
}
```

The rules:

- **The file uses editions**, so it can refer to any source, proto2, proto3 or
  editions, closed enums included. Each field copies its source field's
  features, except presence.
- **Every singular field has explicit presence.** Absent means unchanged.
- **One patch body per message type** the target reaches that isn't atomic. Its
  name comes from the type's nested path: `Team` gives `TeamDiff`, and
  `PlannedSale.Holding` gives `PlannedSale_HoldingDiff`.
- **An atomic message is set whole**; any other message field is typed with its
  patch body. Patching an unset message starts it from empty.
- **Unsetting** a field is done by listing its number in `clear`. Clearing a
  oneof case's field clears that case.
- **A oneof** keeps its cases. A diff that sets a different case from the one
  the target has replaces the oneof whole: `Apply` starts the new case from
  empty.
- **A keyed list** becomes `repeated <Field>Change`, a change wrapper nested in
  the parent's patch body. Its key has the key field's type. A map with scalar
  values has `set` and `remove` instead of `add`, `remove` and `patch`.
- **An ORDERED list** also gets `<field>_order`, a field of the patch body
  that holds the list. Without it, `Apply` keeps surviving items in their base
  order and puts added items after them, in key order. `Diff` writes the
  complete final order of keys there only when the final order differs from
  that default, and `Apply` sorts the list to it.
- **An INDEXED list** becomes `repeated <Field>Edit`, each a base index and
  one of `patch`, `remove` or `insert_before`, which carries the inserted
  elements in order. For scalar elements, `patch` holds the new value. Edits
  name positions in the base list and never shift while being applied, so
  their order in the diff doesn't matter. Each index has at most one insert
  and at most one patch or remove. `Apply` builds the result by walking base
  indexes 0 to n−1: at each index i it emits the insert at i, if any, then
  `base[i]`, patched if patched and left out if removed. Finally it emits the
  insert at n. So an insert at an index comes before that index's element,
  whatever is done to it.
- **A REPLACE list** becomes a wrapper, such as `TagsReplace`, whose presence
  means "use this list".
- **Field numbers match the source.** The generated fields take numbers from
  the top of the range: `clear` at 536870911, the highest protobuf allows, and
  order fields counting down from 536870910, one per ORDERED list, in field
  order.
- **Options record the source.** `change` says what a generated message diffs
  and how, and `field` names each field's source and marks keys and orders.
  With them the runtime needs only the diff's descriptor.

## Using it

**Bazel.** The module provides `proto_diff_library`, a rule used beside
`proto_library` from `@protobuf//bazel`:

```starlark
load("@protobuf//bazel:proto_library.bzl", "proto_library")
load("@protodiffgen//bazel:defs.bzl", "proto_diff_library")

proto_diff_library(
    name = "team_diff_proto",
    config = "team.diff.textproto",
    deps = [":team_proto"],
)
```

It reads its deps' `ProtoInfo.transitive_descriptor_sets`, runs the CLI to
write the `.proto`, compiles it with the proto toolchain, and returns a
`ProtoInfo` of its own. The file is named after the target without a trailing
`_proto`, so `team_diff_proto` in package `examples` writes
`examples/team_diff.proto`. The `.proto` is built, never checked in.

The generated file imports the files whose types it uses, which can be any
file the deps reach, and which ones is known only once the CLI has run. So the
compile of the generated file does not check strict deps. A `proto_library`
that imports the generated file does pass `--strict_proto_deps`, because the
provider's direct source is that file.

`go_proto_library` accepts this `ProtoInfo` as it accepts a `proto_library`'s.
`examples/` builds the Team sample that way, and its test runs `Diff` and
`Apply` on the generated Go types.

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

// Apply validates d against a, then returns a with d applied. a is not changed.
func Apply(a, d proto.Message) (proto.Message, error)

// Validate reports whether d can be applied to a.
func Validate(a, d proto.Message) error

// Overlap lists where two diffs of one base both change something.
func Overlap(d1, d2 proto.Message) ([]Item, error)

// Equal reports whether x and y are equal under the list rules of diff, a
// message of the diff type; diff's contents are ignored.
func Equal(x, y, diff proto.Message) bool

// Item is one place two diffs overlap: an item's path from the root, such as
// members[ada], and the field there, or 0 for the whole item.
type Item struct{ path []Step; field protoreflect.FieldNumber }
func (i Item) Path() []Step
func (i Item) Field() protoreflect.FieldNumber
func (i Item) String() string

// Step is one list item on a path: by key in a keyed list, where an enum key
// is held as its number, or by base index in an indexed one.
type Step struct{ list protoreflect.FullName; key protoreflect.MapKey; index int }
func (s Step) List() protoreflect.FullName
func (s Step) Key() protoreflect.MapKey
func (s Step) Index() int
```

`Validate`, and so `Apply`, refuse a diff that:

- adds a key the list has, or removes or patches one it lacks;
- has two entries for one key, or a change wrapper with no `op`;
- has a wrapper key that differs from its add's key, or a patch that changes
  a keyed item's key;
- clears a field without explicit presence (it is set to its zero value
  instead), or both sets and clears one field;
- has an order field that doesn't name exactly the resulting keys, once each;
- in an indexed list, has a patch or remove index outside [0, n) or an insert
  index outside [0, n], a second insert at one index, two of patch and remove
  at one index, or an insert with no elements;
- leaves an optional key unset in an add;
- or has unknown fields, as `a` may not either.

**Overlap.** An item is the root message or one keyed list item. Two diffs
overlap when both change the same field of the same item, or when one adds,
removes or replaces an ancestor of what the other changes. Setting an atomic
message, a REPLACE list or a new oneof case replaces everything inside it.
Changes to the root are reported per field, so two diffs that change different
top-level fields don't overlap. In an indexed list, two diffs overlap at a base
index when both patch or remove it, when both insert at it, or when one
patches or removes it and the other inserts at it. An insert at n overlaps
only another insert at n.

**Equal.** Two messages are equal when every field is: an UNORDERED keyed list
as a set by key, an ORDERED one in order, INDEXED and REPLACE lists and maps
element by element, recursively. Floats compare by their bits, so NaN equals NaN and
`-0.0` differs from `0.0`. `proto.Equal` won't do: it is order-sensitive.

**Canonical `Diff` for an indexed list.** `Diff` runs a Myers sequence diff
over the two lists, comparing elements with `Equal`. Within each run of
removed-then-added elements, it pairs them by position as patches. Extra
removes stay removes, and extra adds become one `insert_before` at the next
base index that survives, or at n.

## Tests

- **Property tests** over random messages of a test schema with every shape
  above, indexed lists included. `Equal(Apply(a, Diff(a, b)), b)` holds. For
  any `d` that `Validate` accepts, `Diff(a, Apply(a, d))` is `d` in its normal
  form for `a`. In normal form, entries that change nothing in `a` are dropped,
  keyed list changes are in key order, and indexed edits are in index order,
  an insert before a patch or remove at the same index. `Diff` only produces
  diffs in normal form.
- **Golden tests** of the generated `.proto` for the sample config above,
  which has a keyed, an indexed and a replaced list.

## CI

GitHub Actions runs `bazel test //...` and `go test ./...` on every pull
request, with Bazel 9.2.0 and Go 1.25.

## Prior art

`google.protobuf.FieldMask` selects fields, and its documented merge replaces
singular fields and appends repeated ones; C++ `FieldMaskUtil`'s
`MergeOptions::set_replace_repeated_fields` replaces them instead. Neither can
name one list item. Google's AIP-161 forbids index paths, and since its
2026-10-08 revision lists map-key paths as a non-standard extension. JSON
Patch addresses list items by index, which shifts when another change inserts.
JSON Merge Patch replaces arrays whole. Kubernetes' strategic merge patch has
merge keys and is the closest match, but it works on Go structs and JSON, not
on protobuf schemas. For protobuf, the C++ `MessageDifferencer` can compare a
repeated field as a map keyed by one or several fields, and Go's `protocmp`
can sort repeated fields before comparing, but both report differences for a
reader; neither produces a diff to store and apply. No maintained protobuf
library that does keyed list diffs was found.

## Publishing

At first the module is used pinned to a git commit: Bazel's `git_override`,
and `go get github.com/nexteggapp/protodiffgen@<commit>`. After a v0.1 tag it
goes to the Bazel Central Registry.

## Open questions

- **The extension number.** 52000 is a placeholder on both extendees. A number
  should be registered in protobuf's global extension registry before v0.1.
  That is an outside step, for the owner.
- **Preconditions.** A diff applied to anything but its own base may apply
  wrongly, most of all an indexed one. A general mechanism for stating what a
  diff expects of its base could come later.
