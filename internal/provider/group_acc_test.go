//go:build e2e

package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
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
