# protodiffgen

protodiffgen generates typed diff messages for a protobuf message. You give it
a message and a small config saying how each list is matched, and it writes a
`.proto` whose messages describe a change to that message: which fields were
set, which list items were added, removed or patched by key. A Go runtime
computes, applies and checks those diffs by reflection.

Your own `.proto` files need no annotations and no dependency on protodiffgen.
The design, with every rule, is in [`docs/design.md`](docs/design.md).

## The config

```textproto
# team.diff.textproto
target_message: "example.v1.Team"
output_package: "example.v1.diff"
lists { field: "example.v1.Team.members" strategy: KEYED key: "id" order: UNORDERED }
```

Every repeated message field the target reaches needs a rule: `KEYED` by one of
the item's fields, `INDEXED` by position in the base list, or `REPLACE`d whole.
The config is `nextgui.protodiffgen.v1.DiffConfig`, in
[`v1/config.proto`](v1/config.proto).

## With Bazel

```starlark
# MODULE.bazel
bazel_dep(name = "protodiffgen")
git_override(
    module_name = "protodiffgen",
    remote = "https://github.com/nexteggapp/protodiffgen.git",
    commit = "<commit>",
)
```

```starlark
# BUILD.bazel
load("@protodiffgen//bazel:defs.bzl", "proto_diff_library")
load("@rules_go//proto:def.bzl", "go_proto_library")

proto_diff_library(
    name = "team_diff_proto",
    config = "team.diff.textproto",
    deps = [":team_proto"],
)

go_proto_library(
    name = "team_diff_go_proto",
    importpath = "example.com/team/v1/diffpb",
    proto = ":team_diff_proto",
    deps = [
        ":team_go_proto",
        "@protodiffgen//v1:config_go_proto",
    ],
)
```

[`examples/`](examples) builds the design's Team sample this way, and keeps a
copy of the generated `team_diff.proto`: `bazel run //examples:write_team_diff`
updates it, and `bazel test //...` fails if it is stale.

## With buf or protoc

```bash
go install github.com/nexteggapp/protodiffgen/cmd/protodiffgen@<commit>
buf build -o team.binpb    # or: protoc --include_imports --descriptor_set_out=team.binpb ...
protodiffgen --descriptor_set=team.binpb --config=team.diff.textproto --out=team_diff.proto
```

The generated file imports `nextgui.xyz/protodiffgen/v1/config.proto`, so give
your compiler `v1/config.proto` under that path.

## In Go

```go
import "github.com/nexteggapp/protodiffgen/apply"

d := &diffpb.TeamDiff{}
err := apply.Diff(before, after, d) // the smallest change from before to after
next, err := apply.Apply(before, d) // validates d, then returns before with d applied
```

Package `apply` also has `Validate`, `Overlap`, which lists where two diffs of
one base both change something, and `Equal`, which compares two messages under
the list rules.

## Status

v0.1 is built and not yet tagged. The option extension number, 52000, is a
placeholder until it is registered.

License: Apache 2.0. See [`LICENSE`](LICENSE).
