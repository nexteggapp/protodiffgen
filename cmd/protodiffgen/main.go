// Command protodiffgen writes the diff .proto for a target message.
//
//	protodiffgen --descriptor_set=team.binpb --config=team.diff.textproto --out=team_diff.proto
//
// --descriptor_set may be given more than once. Together the sets must hold
// the target's file and every file it imports.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/nexteggapp/protodiffgen/generate"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(s string) error { *l = append(*l, s); return nil }

func main() {
	var sets list
	flag.Var(&sets, "descriptor_set", "a FileDescriptorSet file; repeatable")
	config := flag.String("config", "", "the DiffConfig, in text format")
	out := flag.String("out", "", "the .proto file to write")
	flag.Parse()
	if err := run(sets, *config, *out); err != nil {
		fmt.Fprintln(os.Stderr, "protodiffgen:", err)
		os.Exit(1)
	}
}

func run(sets []string, config, out string) error {
	if len(sets) == 0 || config == "" || out == "" {
		return fmt.Errorf("--descriptor_set, --config and --out are required")
	}
	all := &descriptorpb.FileDescriptorSet{}
	for _, path := range sets {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		set := &descriptorpb.FileDescriptorSet{}
		if err := proto.Unmarshal(b, set); err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		all.File = append(all.File, set.File...)
	}
	text, err := os.ReadFile(config)
	if err != nil {
		return err
	}
	gen, err := generate.File(all, text)
	if err != nil {
		return fmt.Errorf("%s: %v", config, err)
	}
	return os.WriteFile(out, gen, 0o644)
}
