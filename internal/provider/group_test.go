package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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
				Resources: types.ListValueMust(types.StringType, []attr.Value{}),
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
				},
			},
			expected: GroupModel{
				Id:        types.StringValue("def"),
				Issued:    types.StringNull(),
				Name:      types.StringValue("Meow"),
				Peers:     types.ListValueMust(types.StringType, []attr.Value{types.StringValue("c1"), types.StringValue("c2")}),
				Resources: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("r1"), types.StringValue("r2")}),
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
