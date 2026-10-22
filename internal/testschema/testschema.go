// Package testschema compiles .proto sources and their generated diff file
// without protoc, for tests.
package testschema

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/linker"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/nexteggapp/protodiffgen/generate"
	"github.com/nexteggapp/protodiffgen/internal/schema"
)

//go:embed proto config.textproto
var embedded embed.FS

const diffPath = "protodiffgen_test/diff.proto"

// Schema is a compiled set of source files and the diff file generated for
// them.
type Schema struct {
	sources linker.Files
	set     *descriptorpb.FileDescriptorSet
	text    []byte
	diff    linker.File
}

// Set returns the source files and their imports, in dependency order.
func (s *Schema) Set() *descriptorpb.FileDescriptorSet { return s.set }

// Text returns the generated .proto.
func (s *Schema) Text() []byte { return s.text }

// Diff returns the generated file, compiled.
func (s *Schema) Diff() protoreflect.FileDescriptor { return s.diff }

// Message returns a source or generated message by full name.
func (s *Schema) Message(name protoreflect.FullName) protoreflect.MessageDescriptor {
	if d := s.diff.FindDescriptorByName(name); d != nil {
		if md, ok := d.(protoreflect.MessageDescriptor); ok {
			return md
		}
	}
	for _, f := range s.sources {
		if d := f.FindDescriptorByName(name); d != nil {
			if md, ok := d.(protoreflect.MessageDescriptor); ok {
				return md
			}
		}
	}
	return nil
}

// Load builds the embedded test schema in proto/pdgtest/v1 with its config.
func Load() (*Schema, error) {
	sources := map[string]string{}
	err := fs.WalkDir(embedded, "proto", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := embedded.ReadFile(path)
		sources[strings.TrimPrefix(path, "proto/")] = string(b)
		return err
	})
	if err != nil {
		return nil, err
	}
	config, err := embedded.ReadFile("config.textproto")
	if err != nil {
		return nil, err
	}
	return Build(sources, config)
}

// Compile compiles sources, a map from import path to content.
func Compile(sources map[string]string) (linker.Files, error) {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	c := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(sources),
		}),
	}
	return c.Compile(context.Background(), names...)
}

// Build compiles sources, generates the diff file for config, and compiles
// that too.
func Build(sources map[string]string, config []byte) (*Schema, error) {
	files, err := Compile(sources)
	if err != nil {
		return nil, err
	}
	s := &Schema{sources: files, set: setOf(files)}
	s.text, err = generate.File(s.set, config)
	if err != nil {
		return nil, err
	}
	byPath := map[string]linker.File{}
	for _, f := range files {
		byPath[f.Path()] = f
	}
	std := protocompile.WithStandardImports(protocompile.ResolverFunc(func(string) (protocompile.SearchResult, error) {
		return protocompile.SearchResult{}, fs.ErrNotExist
	}))
	c := protocompile.Compiler{
		SourceInfoMode: protocompile.SourceInfoStandard,
		Resolver: protocompile.ResolverFunc(func(path string) (protocompile.SearchResult, error) {
			switch {
			case path == diffPath:
				return protocompile.SearchResult{Source: strings.NewReader(string(s.text))}, nil
			case path == schema.Path:
				return protocompile.SearchResult{Desc: schema.File()}, nil
			case byPath[path] != nil:
				return protocompile.SearchResult{Desc: byPath[path]}, nil
			}
			return std.FindFileByPath(path)
		}),
	}
	out, err := c.Compile(context.Background(), diffPath)
	if err != nil {
		return nil, fmt.Errorf("compiling the generated file: %v\n%s", err, s.text)
	}
	s.diff = out[0]
	return s, nil
}

// setOf returns files and everything they import, in dependency order.
func setOf(files linker.Files) *descriptorpb.FileDescriptorSet {
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	var add func(fd protoreflect.FileDescriptor)
	add = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}
		seen[fd.Path()] = true
		for i := 0; i < fd.Imports().Len(); i++ {
			add(fd.Imports().Get(i).FileDescriptor)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(fd))
	}
	for _, f := range files {
		add(f)
	}
	return set
}
