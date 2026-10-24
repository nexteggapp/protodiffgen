package apply

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Target is a message checked once as the target of one diff type's diffs:
// it has no unknown fields, and each keyed list in it has every item's key
// set and no key twice. [Validate] and [Apply] check that of their target
// on every call, which walks the whole message however small the diff.
// Target.Apply checks only the diff, and returns a Target, since a diff
// that validates leaves the result with the same properties.
//
// A Target holds its message rather than a copy. Neither the message nor
// any message taken from it may be changed while the Target is in use.
type Target struct {
	m proto.Message
	p *plan
}

// NewTarget checks a as the target of diffs whose message is diff, as
// Validate checks its a.
func NewTarget(a proto.Message, diff protoreflect.MessageDescriptor) (Target, error) {
	p, err := planFor(diff)
	if err != nil {
		return Target{}, err
	}
	am := a.ProtoReflect()
	if err := p.bind(am.Descriptor()); err != nil {
		return Target{}, err
	}
	if err := checkData(p, am, "a"); err != nil {
		return Target{}, err
	}
	return Target{m: a, p: p}, nil
}

// Message returns the target's message, not a copy.
func (t Target) Message() proto.Message { return t.m }

// Apply validates d against the target as Validate does, without checking
// the target again, then returns the target with d applied. The target is
// not changed.
func (t Target) Apply(d proto.Message) (Target, error) {
	if t.p == nil {
		return Target{}, errors.New("apply: the Target is the zero value; use NewTarget")
	}
	dm := d.ProtoReflect()
	if dm.Descriptor() != t.p.body {
		return Target{}, fmt.Errorf("apply: the diff is a %s; the Target takes %s", dm.Descriptor().FullName(), t.p.body.FullName())
	}
	if err := validateDiff(t.p, t.m.ProtoReflect(), dm); err != nil {
		return Target{}, err
	}
	out := proto.Clone(t.m)
	apply(t.p, out.ProtoReflect(), dm)
	return Target{m: out, p: t.p}, nil
}
