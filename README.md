# protodiffgen

protodiffgen generates typed diff messages for a protobuf message. You give it
a message and a small config saying how each list is matched, and it writes a
`.proto` whose messages describe a change to that message: which fields were
set, which list items were added, removed or patched by key. A Go runtime
computes, applies and checks those diffs by reflection.

```textproto
# team.diff.textproto
target_message: "example.v1.Team"
output_package: "example.v1.diff"
lists { field: "example.v1.Team.members" strategy: KEYED key: "id" }
```

```go
d := &diffpb.TeamDiff{}
err := apply.Diff(before, after, d) // the smallest change from before to after
next, err := apply.Apply(before, d) // equal to after
```

It has three parts:

- **A Bazel rule**, `proto_diff_library`, which turns a `proto_library` and a
  config into a `proto_library` of the diff. Generated files are built, never
  checked in.
- **A CLI**, `cmd/protodiffgen`, which the rule wraps, for buf and protoc users.
- **A Go package**, `apply`: `Diff`, `Apply`, `Overlap` and `Validate`.

Your own `.proto` files need no annotations and no dependency on protodiffgen.

Status: designed, not built. See [`docs/design.md`](docs/design.md).

Licence: Apache 2.0. See [`LICENSE`](LICENSE).
