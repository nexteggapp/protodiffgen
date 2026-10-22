"""proto_diff_library: the generated diff .proto for a target, as a proto library."""

load("@protobuf//bazel/common:proto_common.bzl", "proto_common")
load("@protobuf//bazel/common:proto_info.bzl", "ProtoInfo")

_PROTO_TOOLCHAIN = Label("@protobuf//bazel/private:proto_toolchain_type")

def _proto_diff_library_impl(ctx):
    name = ctx.label.name.removesuffix("_proto")
    out = ctx.actions.declare_file(name + ".proto")
    deps = [d[ProtoInfo] for d in ctx.attr.deps]
    sets = depset(transitive = [d.transitive_descriptor_sets for d in deps])

    args = ctx.actions.args()
    args.add_all(sets, format_each = "--descriptor_set=%s")
    args.add("--config", ctx.file.config)
    args.add("--out", out)
    ctx.actions.run(
        executable = ctx.executable._generator,
        arguments = [args],
        inputs = depset([ctx.file.config], transitive = [sets]),
        outputs = [out],
        mnemonic = "ProtoDiffGen",
        progress_message = "Generating %{output}",
    )

    # The generated file imports config.proto and empty.proto, and files
    # that deps reach.
    all_deps = deps + [ctx.attr._config[ProtoInfo], ctx.attr._empty[ProtoInfo]]
    descriptor_set = ctx.actions.declare_file(name + "-descriptor-set.proto.bin")
    proto_info = ProtoInfo(
        srcs = [out],
        deps = all_deps,
        descriptor_set = descriptor_set,
    )
    if proto_common.INCOMPATIBLE_ENABLE_PROTO_TOOLCHAIN_RESOLUTION:
        toolchain = ctx.toolchains[_PROTO_TOOLCHAIN]
        if not toolchain:
            fail("No proto toolchain is registered.")
        lang = toolchain.proto
    else:
        lang = proto_common.ProtoLangToolchainInfo(
            out_replacement_format_flag = "--descriptor_set_out=%s",
            output_files = "single",
            mnemonic = "GenProtoDescriptorSet",
            progress_message = "Generating descriptor set for %{label}",
            proto_compiler = ctx.executable._proto_compiler,
            protoc_opts = [],
            plugin = None,
        )
    proto_common.compile(
        ctx.actions,
        proto_info,
        lang,
        generated_files = [descriptor_set],
        additional_inputs = depset(transitive = [d.transitive_descriptor_sets for d in all_deps]),
    )
    return [
        proto_info,
        DefaultInfo(files = depset([out, descriptor_set])),
    ]

_attrs = dict({
    "config": attr.label(
        mandatory = True,
        allow_single_file = True,
        doc = "A DiffConfig in text format.",
    ),
    "deps": attr.label_list(
        providers = [ProtoInfo],
        doc = "proto_library targets that hold the target message and what it reaches.",
    ),
    "_generator": attr.label(
        default = Label("//cmd/protodiffgen"),
        executable = True,
        cfg = "exec",
    ),
    "_config": attr.label(
        default = Label("//v1:config_proto"),
        providers = [ProtoInfo],
    ),
    "_empty": attr.label(
        default = Label("@protobuf//:empty_proto"),
        providers = [ProtoInfo],
    ),
}, **({} if proto_common.INCOMPATIBLE_ENABLE_PROTO_TOOLCHAIN_RESOLUTION else {
    "_proto_compiler": attr.label(
        default = Label("@protobuf//:protoc"),
        executable = True,
        cfg = "exec",
    ),
}))

proto_diff_library = rule(
    implementation = _proto_diff_library_impl,
    doc = """Generates the diff .proto for the target a DiffConfig names.

The file is named after the target, without a trailing "_proto": a target
team_diff_proto in package examples writes examples/team_diff.proto. The rule
returns a ProtoInfo, so language rules such as go_proto_library take it as
they take a proto_library.
""",
    attrs = _attrs,
    toolchains = [config_common.toolchain_type(_PROTO_TOOLCHAIN, mandatory = False)] if proto_common.INCOMPATIBLE_ENABLE_PROTO_TOOLCHAIN_RESOLUTION else [],
    provides = [ProtoInfo],
)
