//go:build e2e

package provider

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/netbirdio/netbird/shared/management/http/api"
)

func Test_Group_Create(t *testing.T) {
	testE2E(t)
	rName := "g" + acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)
	rNameFull := "netbird_group." + rName
	var createdID string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testEnsureManagementRunning(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testCheckGone(testClient().Groups.Get, &createdID),
		Steps: []resource.TestStep{
			{
				ResourceName: rName,
				Config:       testGroupResource(rName, `[]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					testRecordID(rNameFull, &createdID),
					resource.TestCheckResourceAttrSet(rNameFull, "id"),
					resource.TestCheckResourceAttr(rNameFull, "name", rName),
					func(s *terraform.State) error {
						gID := s.RootModule().Resources[rNameFull].Primary.Attributes["id"]
						group, err := testClient().Groups.Get(context.Background(), gID)
						if err != nil {
							return err
						}
						if group.Name != rName {
							return fmt.Errorf("Group name mismatch, expected %s, found %s on management server", rName, group.Name)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      rNameFull,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func Test_Group_Update(t *testing.T) {
	testE2E(t)
	rName := "g" + acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)
	rNameFull := "netbird_group." + rName
	// Resolved once, up front: the fixture helpers can fail the test, and doing
	// that from inside a Check closure aborts the run mid-apply, before
	// terraform-plugin-testing gets to its destroy step.
	peerID := testPeerID(t, "peer1")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testEnsureManagementRunning(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			groups, err := testClient().Groups.List(context.Background())
			if err != nil {
				return err
			}
			for _, g := range groups {
				if g.Name == rName {
					return fmt.Errorf("Group not deleted")
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				ResourceName: rName,
				Config:       testGroupResource(rName, `[]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(rNameFull, "id"),
					resource.TestCheckResourceAttr(rNameFull, "name", rName),
				),
			},
			{
				ResourceName: rName,
				Config:       testGroupResource(rName, fmt.Sprintf("[%q]", peerID)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(rNameFull, "id"),
					resource.TestCheckResourceAttr(rNameFull, "name", rName),
					resource.TestCheckResourceAttr(rNameFull, "peers.#", "1"),
					resource.TestCheckResourceAttr(rNameFull, "peers.0", peerID),
					func(s *terraform.State) error {
						gID := s.RootModule().Resources[rNameFull].Primary.Attributes["id"]
						group, err := testClient().Groups.Get(context.Background(), gID)
						if err != nil {
							return err
						}
						if len(group.Peers) != 1 {
							return fmt.Errorf("Group Peers not updated in management")
						}
						if group.Peers[0].Id != peerID {
							return fmt.Errorf("Group Peers incorrect")
						}
						return nil
					},
				),
			},
		},
	})
}

func testGroupResource(rName, peers string) string {
	return fmt.Sprintf(`resource "netbird_group" "%s" {
	name = "%s"
	peers = %s
}`, rName, rName, peers)
}

// issuedRejected is the validator's own wording. It names the attribute, quotes
// the enum the schema allows and prints the value that was refused, so a failure
// from anywhere else in the pipeline does not match it.
var issuedRejected = regexp.MustCompile(
	`(?s)Invalid Attribute Value Match.*Attribute issued value must be one of: \["api" "jwt"\], got: "integration"`)

// Test_Group_Issued covers the attribute the server resolves rather than the
// configuration. Left out, management picks the value and the provider has to
// adopt what it picked instead of planning the attribute away; set, it selects
// where the group's members come from, and changing it is an update rather than a
// replacement, so a group other resources already reference keeps its identity
// while it becomes IdP-synced.
func Test_Group_Issued(t *testing.T) {
	testE2E(t)
	rName := "g" + acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)
	address := "netbird_group." + rName
	var id string

	// State cannot tell a server that agreed from one that ignored the request:
	// the read maps whatever it finds back into the attribute either way.
	issuedOnServer := func(want api.GroupIssued) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			rs, ok := s.RootModule().Resources[address]
			if !ok {
				return fmt.Errorf("%s is not in state", address)
			}
			group, err := testClient().Groups.Get(context.Background(), rs.Primary.Attributes["id"])
			if err != nil {
				return err
			}
			if group.Issued == nil {
				return fmt.Errorf("management reports no issued value on the group, expected %q", want)
			}
			if *group.Issued != want {
				return fmt.Errorf("management has issued %q on the group, expected %q", *group.Issued, want)
			}
			return nil
		}
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testEnsureManagementRunning(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testCheckGone(testClient().Groups.Get, &id),
		Steps: []resource.TestStep{
			{
				// The API knows a third origin, integration, that a caller may
				// not ask for. This step goes first because the run's implicit
				// destroy evaluates the last configuration it was given, and a
				// refused value left in the working directory fails there rather
				// than here.
				Config:      testGroupResourceIssued(rName, "integration"),
				ExpectError: issuedRejected,
			},
			{
				// Nothing but a name: the server defaults the attribute, and the
				// provider adopts the default rather than sending an empty value
				// or planning one back. It also carries the proof that the
				// refused step above reached no server: a group it had created
				// would still be carrying integration, since a configuration
				// that omits the attribute keeps the value of the group.
				Config: testGroupResourceNameOnly(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					testRecordID(address, &id),
					resource.TestCheckResourceAttr(address, "issued", "api"),
					issuedOnServer(api.GroupIssuedApi),
				),
			},
			{
				Config:           testGroupResourceIssued(rName, "jwt"),
				ConfigPlanChecks: updatesInPlace(address),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "issued", "jwt"),
					resource.TestCheckResourceAttrPtr(address, "id", &id),
					issuedOnServer(api.GroupIssuedJwt),
				),
			},
			{
				Config:           testGroupResourceIssued(rName, "api"),
				ConfigPlanChecks: updatesInPlace(address),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "issued", "api"),
					resource.TestCheckResourceAttrPtr(address, "id", &id),
					issuedOnServer(api.GroupIssuedApi),
				),
			},
			{
				ResourceName:      address,
				ImportState:       true,
				ImportStateVerify: true,
				Check:             resource.TestCheckResourceAttr(address, "issued", "api"),
			},
		},
	})
}

// testGroupResourceNameOnly configures a group with nothing but its name, so the
// server resolves the rest.
func testGroupResourceNameOnly(rName string) string {
	return fmt.Sprintf(`resource "netbird_group" "%s" {
	name = "%s"
}`, rName, rName)
}

func testGroupResourceIssued(rName, issued string) string {
	return fmt.Sprintf(`resource "netbird_group" "%s" {
	name   = "%s"
	issued = "%s"
}`, rName, rName, issued)
}

// resourcePair is one entry of a group's resources attribute: the id of the
// resource and the kind it names.
type resourcePair struct {
	id   string
	kind string
}

// Test_Group_Resources covers resources as a list of id/type pairs. Every kind
// the enum allows is set at once first, then two pairs are dropped and the rest
// reordered, because a mapping that merged instead of replacing, or sorted the
// list, would pass a test that never changed the set. State checks pin the order
// the provider read back; the comparison against management is a set one, since
// the API does not promise an order.
func Test_Group_Resources(t *testing.T) {
	testE2E(t)
	rName := "g" + acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)
	rNameFull := "netbird_group." + rName
	dsNameFull := "data.netbird_group." + rName

	// Resolved once, up front: the fixture helpers can fail the test, and doing
	// that from inside a Check closure aborts the run mid-apply, before
	// terraform-plugin-testing gets to its destroy step.
	peer := resourcePair{id: testPeerID(t, "peer2"), kind: "peer"}
	domain := resourcePair{id: e2eResourceDomainID(), kind: "domain"}
	subnet := resourcePair{id: e2eResourceSubnetID(), kind: "subnet"}
	host := resourcePair{id: e2eResourceHostID(), kind: "host"}

	all := []resourcePair{peer, domain, subnet, host}
	kept := []resourcePair{subnet, peer}

	var createdID string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testEnsureManagementRunning(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testCheckGone(testClient().Groups.Get, &createdID),
		Steps: []resource.TestStep{
			{
				Config: testGroupResourceResources(rName, all),
				Check: resource.ComposeAggregateTestCheckFunc(
					testRecordID(rNameFull, &createdID),
					resource.TestCheckResourceAttrSet(rNameFull, "id"),
					resource.TestCheckResourceAttr(rNameFull, "name", rName),
					checkResourcesInOrder(rNameFull, all),
					groupResourcesOnServer(t, rNameFull, all),
				),
			},
			{
				// The data source shares the resource's model, so its resources
				// attribute has to report the same pairs. Its block depends on
				// the resource because the name it selects on is known at plan
				// time, and without the edge Terraform would read the group
				// before the update rewrote it.
				Config: testGroupResourceResources(rName, kept) + testGroupDataSource(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPtr(rNameFull, "id", &createdID),
					checkResourcesInOrder(rNameFull, kept),
					groupResourcesOnServer(t, rNameFull, kept),
					checkResourcesInOrder(dsNameFull, kept),
				),
			},
			{
				ResourceName:      rNameFull,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Idempotency: the refreshed state holds exactly the pairs the
				// configuration sets, so nothing is left to plan. A PlanOnly step
				// skips its first plan and apply, which is why the check hangs
				// off PostApplyPreRefresh: that is where the step's plan checks
				// run against the non-refresh plan.
				Config:   testGroupResourceResources(rName, kept) + testGroupDataSource(rName),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Management refuses to delete a group that still holds any
				// resource ("group has been linked to network resource"), and
				// the provider's delete does not clear them first, so a group
				// left populated cannot be destroyed in one go: it has to be
				// drained by an apply, then destroyed. This step does that
				// through the empty-list path, which is the clearing behaviour
				// worth pinning on its own, and leaves the group deletable for
				// the destroy check.
				Config: testGroupResourceResources(rName, nil),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkResourcesInOrder(rNameFull, nil),
					groupResourcesOnServer(t, rNameFull, nil),
				),
			},
		},
	})
}

// testGroupResourceResources configures a group whose resources attribute holds
// the given pairs, in the given order.
func testGroupResourceResources(rName string, pairs []resourcePair) string {
	entries := make([]string, len(pairs))
	for i, p := range pairs {
		entries[i] = fmt.Sprintf("{ id = %q, type = %q }", p.id, p.kind)
	}
	return fmt.Sprintf(`resource "netbird_group" "%s" {
	name      = "%s"
	resources = [%s]
}`, rName, rName, strings.Join(entries, ", "))
}

// testGroupDataSource reads this test's group back by name, so the data source
// and the resource can be checked against the same server-side pairs.
func testGroupDataSource(rName string) string {
	return fmt.Sprintf(`
data "netbird_group" "%[1]s" {
  name       = netbird_group.%[1]s.name
  depends_on = [netbird_group.%[1]s]
}
`, rName)
}

// checkResourcesInOrder asserts the state's resources attribute holds exactly
// the pairs given, in the order given. The provider reads the list back from the
// API, so the order under test is management's echo of the request.
func checkResourcesInOrder(address string, pairs []resourcePair) resource.TestCheckFunc {
	checks := []resource.TestCheckFunc{
		resource.TestCheckResourceAttr(address, "resources.#", strconv.Itoa(len(pairs))),
	}
	for i, p := range pairs {
		checks = append(checks,
			resource.TestCheckResourceAttr(address, fmt.Sprintf("resources.%d.id", i), p.id),
			resource.TestCheckResourceAttr(address, fmt.Sprintf("resources.%d.type", i), p.kind),
		)
	}
	return resource.ComposeAggregateTestCheckFunc(checks...)
}

// groupResourcesOnServer asks management for the group and compares its
// resource pairs with the configured ones. The API does not promise the order it
// returns resources in, so the comparison is by set: a reordering is noted, not
// failed, while a missing, extra or re-typed pair fails with both sets printed,
// so a normalised type shows up as the mismatch it is.
func groupResourcesOnServer(t *testing.T, address string, want []resourcePair) resource.TestCheckFunc {
	wantSet := make(map[string]string, len(want))
	wantOrder := make([]string, 0, len(want))
	for _, p := range want {
		wantSet[p.id] = p.kind
		wantOrder = append(wantOrder, p.kind+":"+p.id)
	}

	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[address]
		if !ok {
			return fmt.Errorf("%s is not in state", address)
		}
		group, err := testClient().Groups.Get(context.Background(), rs.Primary.Attributes["id"])
		if err != nil {
			return err
		}

		gotSet := make(map[string]string, len(group.Resources))
		gotOrder := make([]string, 0, len(group.Resources))
		for _, res := range group.Resources {
			gotSet[res.Id] = string(res.Type)
			gotOrder = append(gotOrder, string(res.Type)+":"+res.Id)
		}

		if len(group.Resources) != len(gotSet) {
			return fmt.Errorf("management returned %d group resources but only %d distinct ids: %v",
				len(group.Resources), len(gotSet), pairStrings(gotSet))
		}
		if !maps.Equal(gotSet, wantSet) {
			return fmt.Errorf("management holds group resources %v, expected %v",
				pairStrings(gotSet), pairStrings(wantSet))
		}
		if !slices.Equal(gotOrder, wantOrder) {
			// Order is not part of the API contract, so this is a note rather
			// than a failure; it is here because a silent reorder would make the
			// state-order checks above look like a provider bug.
			t.Logf("management returned the group resources in %v, configuration set %v", gotOrder, wantOrder)
		}
		return nil
	}
}

// pairStrings renders a resource pair set as a sorted slice, so a failing check
// names the difference instead of printing a map in random order.
func pairStrings(pairs map[string]string) []string {
	out := make([]string, 0, len(pairs))
	for id, kind := range pairs {
		out = append(out, kind+":"+id)
	}
	slices.Sort(out)
	return out
}
