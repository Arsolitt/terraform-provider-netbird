package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/netbirdio/netbird/shared/management/http/api"
)

func Test_groupAPIToTerraform(t *testing.T) {
	cases := []struct {
		resource *api.Group
		expected GroupModel
	}{
		{
			resource: &api.Group{
				Id:             "abc",
				Issued:         valPtr(api.GroupIssuedApi),
				Name:           "Test",
				Peers:          []api.PeerMinimum{},
				PeersCount:     0,
				Resources:      []api.Resource{},
				ResourcesCount: 0,
			},
			expected: GroupModel{
				Id:        types.StringValue("abc"),
				Issued:    types.StringValue("api"),
				Name:      types.StringValue("Test"),
				Peers:     types.ListValueMust(types.StringType, []attr.Value{}),
				Resources: types.ListValueMust(GroupResourceModel{}.TFType(), []attr.Value{}),
			},
		},
		{
			resource: &api.Group{
				Id:     "def",
				Issued: nil,
				Name:   "Meow",
				Peers: []api.PeerMinimum{
					{
						Id:   "c1",
						Name: "Useless",
					},
					{
						Id:   "c2",
						Name: "Also useless",
					},
				},
				PeersCount: 2,
				Resources: []api.Resource{
					{
						Id:   "r1",
						Type: api.ResourceTypeDomain,
					},
					{
						Id:   "r2",
						Type: api.ResourceTypeSubnet,
					},
					{
						Id:   "r3",
						Type: api.ResourceTypeHost,
					},
					{
						Id:   "r4",
						Type: api.ResourceTypePeer,
					},
				},
			},
			expected: GroupModel{
				Id:     types.StringValue("def"),
				Issued: types.StringNull(),
				Name:   types.StringValue("Meow"),
				Peers:  types.ListValueMust(types.StringType, []attr.Value{types.StringValue("c1"), types.StringValue("c2")}),
				Resources: types.ListValueMust(GroupResourceModel{}.TFType(), []attr.Value{
					types.ObjectValueMust(GroupResourceModel{}.TFType().AttrTypes, map[string]attr.Value{
						"id":   types.StringValue("r1"),
						"type": types.StringValue("domain"),
					}),
					types.ObjectValueMust(GroupResourceModel{}.TFType().AttrTypes, map[string]attr.Value{
						"id":   types.StringValue("r2"),
						"type": types.StringValue("subnet"),
					}),
					types.ObjectValueMust(GroupResourceModel{}.TFType().AttrTypes, map[string]attr.Value{
						"id":   types.StringValue("r3"),
						"type": types.StringValue("host"),
					}),
					types.ObjectValueMust(GroupResourceModel{}.TFType().AttrTypes, map[string]attr.Value{
						"id":   types.StringValue("r4"),
						"type": types.StringValue("peer"),
					}),
				}),
			},
		},
	}

	for _, c := range cases {
		var out GroupModel
		outDiag := groupAPIToTerraform(context.Background(), c.resource, &out)
		if outDiag.HasError() {
			t.Fatalf("Expected no error diagnostics, found %d errors", outDiag.ErrorsCount())
		}

		if !reflect.DeepEqual(out, c.expected) {
			t.Fatalf("Expected:\n%#v\nFound:\n%#v", c.expected, out)
		}
	}
}

// Test_groupRequestIssued pins the mapping of the issued attribute onto the
// request. The field is Optional and Computed, so a configuration that leaves it
// out plans it as unknown rather than null, and both unset states have to reach
// the API as an omitted field: the request builder carries the enum only when the
// caller set a value the request endpoint accepts. A value such as "integration",
// which can only come from state written by an integration, has to be omitted the
// same way: the endpoint rejects it, and leaving the field out is what makes the
// server keep the group's existing origin.
func Test_groupRequestIssued(t *testing.T) {
	cases := []struct {
		name     string
		value    types.String
		expected *api.GroupRequestIssued
	}{
		{
			name:  "null",
			value: types.StringNull(),
		},
		{
			// Not the same state as null, and the one a guard written against
			// v.ValueString() gets wrong: that call returns "" for an unknown
			// String, so the request would name an origin the caller never set
			// instead of leaving the field out for the server to fill in.
			name:  "unknown",
			value: types.StringUnknown(),
		},
		{
			name:     "api",
			value:    types.StringValue("api"),
			expected: valPtr(api.GroupRequestIssuedApi),
		},
		{
			name:     "jwt",
			value:    types.StringValue("jwt"),
			expected: valPtr(api.GroupRequestIssuedJwt),
		},
		{
			// An integration-managed group read back into state carries this
			// origin, and an unrelated update must not echo it to the endpoint.
			name:  "integration",
			value: types.StringValue("integration"),
		},
		{
			name:  "empty",
			value: types.StringValue(""),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := groupRequestIssued(c.value)
			if !reflect.DeepEqual(got, c.expected) {
				t.Fatalf("Expected:\n%#v\nFound:\n%#v", c.expected, got)
			}
		})
	}

	t.Run("unrepresentable values are omitted, accepted ones round-trip", func(t *testing.T) {
		// The control for the cases above: everything the request enum cannot
		// express, whether unset (null, unknown) or a value outside the enum
		// (the empty string, an integration origin), must be omitted alike so
		// the server keeps the existing origin instead of rejecting the update.
		for _, unrepresentable := range []types.String{
			types.StringNull(),
			types.StringUnknown(),
			types.StringValue(""),
			types.StringValue("integration"),
		} {
			if got := groupRequestIssued(unrepresentable); got != nil {
				t.Errorf("Expected %s to be omitted, found %#v", unrepresentable, got)
			}
		}

		// The guard must not swallow the origins the request enum does accept.
		for value, want := range map[string]api.GroupRequestIssued{
			"api": api.GroupRequestIssuedApi,
			"jwt": api.GroupRequestIssuedJwt,
		} {
			got := groupRequestIssued(types.StringValue(value))
			if got == nil || *got != want {
				t.Errorf("Expected %q to round-trip to %q, found %#v", value, want, got)
			}
		}
	})
}

// Test_groupResourcesTerraformToAPI pins the request mapping of the resources
// attribute. Every entry has to reach the API as an id/type pair, while the
// unset states have to leave the field out of the request: an Optional+Computed
// attribute the configuration does not mention is unknown in the plan, and
// management reads an omitted field as an empty set (the default on create, a
// clear on update). The refreshed state carries the server's own pairs, so an
// untouched group round-trips them.
func Test_groupResourcesTerraformToAPI(t *testing.T) {
	cases := []struct {
		name     string
		list     types.List
		expected *[]api.Resource
	}{
		{
			name: "null",
			list: types.ListNull(GroupResourceModel{}.TFType()),
		},
		{
			name: "unknown",
			list: types.ListUnknown(GroupResourceModel{}.TFType()),
		},
		{
			name: "empty",
			list: types.ListValueMust(GroupResourceModel{}.TFType(), []attr.Value{}),
		},
		{
			name: "entries",
			list: types.ListValueMust(GroupResourceModel{}.TFType(), []attr.Value{
				types.ObjectValueMust(GroupResourceModel{}.TFType().AttrTypes, map[string]attr.Value{
					"id":   types.StringValue("peer-1"),
					"type": types.StringValue("peer"),
				}),
				types.ObjectValueMust(GroupResourceModel{}.TFType().AttrTypes, map[string]attr.Value{
					"id":   types.StringValue("subnet-1"),
					"type": types.StringValue("subnet"),
				}),
			}),
			expected: &[]api.Resource{
				{Id: "peer-1", Type: api.ResourceTypePeer},
				{Id: "subnet-1", Type: api.ResourceTypeSubnet},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, diags := groupResourcesTerraformToAPI(context.Background(), c.list)
			if diags.HasError() {
				t.Fatalf("Expected no error diagnostics, found %d errors", diags.ErrorsCount())
			}

			if !reflect.DeepEqual(got, c.expected) {
				t.Fatalf("Expected:\n%#v\nFound:\n%#v", c.expected, got)
			}
		})
	}
}

// Test_groupResourcesStateV0ToV1 pins the state upgrade of the resources
// attribute. Version 0 stored bare ids, and the type the API requires per entry
// cannot be recovered from an id alone, so a non-empty list is dropped to null
// for the next read to refill, while the unset shapes are kept.
func Test_groupResourcesStateV0ToV1(t *testing.T) {
	cases := []struct {
		name string
		v0   types.List
		want types.List
	}{
		{
			name: "null stays null",
			v0:   types.ListNull(types.StringType),
			want: types.ListNull(GroupResourceModel{}.TFType()),
		},
		{
			name: "unknown becomes null",
			v0:   types.ListUnknown(types.StringType),
			want: types.ListNull(GroupResourceModel{}.TFType()),
		},
		{
			name: "empty stays empty",
			v0:   types.ListValueMust(types.StringType, []attr.Value{}),
			want: types.ListValueMust(GroupResourceModel{}.TFType(), []attr.Value{}),
		},
		{
			name: "ids cannot be carried over without their type",
			v0: types.ListValueMust(types.StringType, []attr.Value{
				types.StringValue("r1"),
				types.StringValue("r2"),
			}),
			want: types.ListNull(GroupResourceModel{}.TFType()),
		},
	}

	ctx := context.Background()

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := groupResourcesStateV0ToV1(c.v0)

			if !got.Equal(c.want) {
				t.Fatalf("Expected:\n%#v\nFound:\n%#v", c.want, got)
			}

			// Equal ignores the element type of null and unknown lists, and the
			// element type is the part of the value the upgrade has to change.
			if !got.ElementType(ctx).Equal(c.want.ElementType(ctx)) {
				t.Fatalf("Expected element type %#v, found %#v", c.want.ElementType(ctx), got.ElementType(ctx))
			}
		})
	}
}

// Test_groupUpgradeStateV0 drives the version 0 upgrader the way the framework
// does: the frozen prior schema has to decode state written before resources
// became a nested list, and the upgraded state has to match the current schema,
// carrying the other attributes over and leaving resources for the next read.
func Test_groupUpgradeStateV0(t *testing.T) {
	ctx := context.Background()

	var schemaResp resource.SchemaResponse
	(&Group{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("Expected no error diagnostics building the schema, found %d errors", schemaResp.Diagnostics.ErrorsCount())
	}

	upgrader, ok := (&Group{}).UpgradeState(ctx)[0]
	if !ok {
		t.Fatal("Expected the group resource to register a version 0 state upgrader")
	}

	if upgrader.PriorSchema == nil {
		t.Fatal("Expected the version 0 state upgrader to declare the prior schema")
	}

	prior := tfsdk.State{Schema: *upgrader.PriorSchema}
	priorDiags := prior.Set(ctx, groupModelV0{
		Id:        types.StringValue("g1"),
		Name:      types.StringValue("Test"),
		Issued:    types.StringValue("api"),
		Peers:     types.ListValueMust(types.StringType, []attr.Value{types.StringValue("p1")}),
		Resources: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("r1")}),
	})
	if priorDiags.HasError() {
		t.Fatalf("Expected no error diagnostics writing the prior state, found %d errors", priorDiags.ErrorsCount())
	}

	resp := resource.UpgradeStateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &prior}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Expected no error diagnostics upgrading, found %d errors", resp.Diagnostics.ErrorsCount())
	}

	var got GroupModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Expected the upgraded state to decode as the current model, found %d errors", resp.Diagnostics.ErrorsCount())
	}

	if got.Id.ValueString() != "g1" || got.Name.ValueString() != "Test" || got.Issued.ValueString() != "api" {
		t.Fatalf("Expected the group attributes to be carried over, found %#v", got)
	}

	if !got.Peers.Equal(types.ListValueMust(types.StringType, []attr.Value{types.StringValue("p1")})) {
		t.Fatalf("Expected the peer ids to be carried over, found %#v", got.Peers)
	}

	if !got.Resources.IsNull() || !got.Resources.ElementType(ctx).Equal(GroupResourceModel{}.TFType()) {
		t.Fatalf("Expected the resources to be dropped to a null nested list, found %#v", got.Resources)
	}
}
