package examples_test

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"example.com/team/v1/diffpb"
	"example.com/team/v1/teampb"

	"github.com/nexteggapp/protodiffgen/apply"
)

func TestDiffThenApply(t *testing.T) {
	before := &teampb.Team{
		Name: "Core",
		Members: []*teampb.Member{
			{Id: "ada", Name: "Ada", Addresses: []*teampb.Address{{Line: "1 Main St"}}},
			{Id: "bob", Name: "Bob"},
		},
		Tags: []string{"infra"},
	}
	after := proto.Clone(before).(*teampb.Team)
	ada := after.Members[0]
	ada.Name = "Ada L."
	ada.Addresses = append(ada.Addresses, &teampb.Address{Line: "2 Side St"})
	after.Members = []*teampb.Member{{Id: "cy", Role: teampb.Role_ROLE_LEAD}, ada}
	after.Budget = &teampb.Money{CurrencyCode: "USD", Units: 10}

	d := &diffpb.TeamDiff{}
	if err := apply.Diff(before, after, d); err != nil {
		t.Fatal(err)
	}
	if got := d.GetMembersOrder(); !slices.Equal(got, []string{"cy", "ada"}) {
		t.Errorf("members_order is %v", got)
	}
	if d.GetBudget().GetUnits() != 10 || d.GetName() != "" || d.Name != nil {
		t.Errorf("unexpected diff %v", d)
	}

	// A diff is an ordinary message: it survives the wire.
	b, err := proto.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	stored := &diffpb.TeamDiff{}
	if err := proto.Unmarshal(b, stored); err != nil {
		t.Fatal(err)
	}
	next, err := apply.Apply(before, stored)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(next, after) || !apply.Equal(next, after, stored) {
		t.Errorf("Apply(before, Diff(before, after)) = %v, want %v", next, after)
	}
	if len(before.Members) != 2 {
		t.Error("Apply changed before")
	}

	bad := &diffpb.TeamDiff{Members: []*diffpb.TeamDiff_MembersChange{{
		Id: proto.String("zed"),
		Op: &diffpb.TeamDiff_MembersChange_Remove{},
	}}}
	if _, err := apply.Apply(before, bad); err == nil {
		t.Error("Apply accepted a remove of a missing key")
	}
}
