package apply

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

// TestTargetMatchesApply wants Target.Apply to give what Apply gives, the
// same result or the same refusal, for random targets and diffs.
func TestTargetMatchesApply(t *testing.T) {
	md, p := root(t)
	g := newGen(p, 3)
	for i := 0; i < cases; i++ {
		a := g.message(md, 4)
		d := g.diff(p, a, 4)
		if g.chance(0.2) {
			d = g.diff(p, g.message(md, 4), 4) // often doesn't apply to a
		}
		target, err := NewTarget(a.Interface(), p.body)
		if err != nil {
			t.Fatalf("case %d: NewTarget: %v\na: %s", i, err, text(a))
		}
		want, wantErr := Apply(a.Interface(), d.Interface())
		got, gotErr := target.Apply(d.Interface())
		switch {
		case (wantErr == nil) != (gotErr == nil) || wantErr != nil && wantErr.Error() != gotErr.Error():
			t.Fatalf("case %d: Apply's error is %v and Target.Apply's %v\na: %s\nd: %s", i, wantErr, gotErr, text(a), text(d))
		case wantErr == nil && !proto.Equal(want, got.Message()):
			t.Fatalf("case %d: Target.Apply's result differs from Apply's\na: %s\nd: %s", i, text(a), text(d))
		}
	}
}

// TestTargetStaysChecked applies a run of random diffs through Targets and
// wants each result to pass the check NewTarget makes, which Target.Apply
// relies on without making again.
func TestTargetStaysChecked(t *testing.T) {
	md, p := root(t)
	g := newGen(p, 4)
	for i := 0; i < cases/10; i++ {
		target, err := NewTarget(g.message(md, 4).Interface(), p.body)
		if err != nil {
			t.Fatal(err)
		}
		for step := 0; step < 10; step++ {
			d := g.diff(p, target.Message().ProtoReflect(), 4)
			next, err := target.Apply(d.Interface())
			if err != nil {
				t.Fatalf("case %d step %d: %v\nd: %s", i, step, err, text(d))
			}
			if err := checkData(p, next.Message().ProtoReflect(), "a"); err != nil {
				t.Fatalf("case %d step %d: the result fails the target check: %v\nd: %s", i, step, err, text(d))
			}
			target = next
		}
	}
}

// TestTargetLeavesItsMessage wants Target.Apply to leave the target's
// message as it was.
func TestTargetLeavesItsMessage(t *testing.T) {
	_, p := root(t)
	a := rootOf(t, `name: "r" members { id: "a" name: "Ada" }`)
	before := proto.Clone(a)
	target, err := NewTarget(a, p.body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Apply(diffOf(t, `name: "s" members { id: "a" patch { name: "Bea" } }`)); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(a, before) {
		t.Error("Target.Apply changed the target's message")
	}
}

// TestTargetRefusals checks what NewTarget and Target.Apply refuse.
func TestTargetRefusals(t *testing.T) {
	_, p := root(t)
	dup := rootOf(t, `members { id: "a" } members { id: "a" }`)
	if _, err := NewTarget(dup, p.body); err == nil || !strings.Contains(err.Error(), "a.members: two items have the key a") {
		t.Errorf("NewTarget with a repeated key: %v", err)
	}
	unknown := rootOf(t, `settings { theme: "t" }`)
	addUnknown(unknown.ProtoReflect().Mutable(unknown.ProtoReflect().Descriptor().Fields().ByName("settings")).Message())
	if _, err := NewTarget(unknown, p.body); err == nil || err.Error() != "a.settings has unknown fields" {
		t.Errorf("NewTarget with unknown fields: %v", err)
	}
	if _, err := (Target{}).Apply(diffOf(t, ``)); err == nil {
		t.Error("the zero Target applied a diff")
	}
	target, err := NewTarget(rootOf(t, ``), p.body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Apply(rootOf(t, ``)); err == nil || !strings.Contains(err.Error(), "the Target takes pdgtest.v1.diff.RootDiff") {
		t.Errorf("a diff of the wrong type: %v", err)
	}
}
