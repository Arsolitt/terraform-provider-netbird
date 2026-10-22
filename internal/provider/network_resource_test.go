package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/netbirdio/netbird/shared/management/http/api"
)

func Test_networkResourceAPIToTerraform(t *testing.T) {
	cases := []struct {
		resource *api.NetworkResource
		expected NetworkResourceModel
	}{
		{
			resource: &api.NetworkResource{
				Address:     "1.1.1.1/32",
				Description: nil,
				Enabled:     false,
				Groups:      []api.GroupMinimum{},
				Id:          "r1",
				Name:        "test",
				Type:        api.NetworkResourceTypeSubnet,
			},
			expected: NetworkResourceModel{
				Id:          types.StringValue("r1"),
				Name:        types.StringValue("test"),
				Description: types.StringNull(),
				Address:     types.StringValue("1.1.1.1/32"),
				Enabled:     types.BoolValue(false),
				Groups:      types.SetNull(types.StringType),
			},
		},
		{
			resource: &api.NetworkResource{
				Address:     "10.0.0.0/24",
				Description: nil,
				Enabled:     true,
				Groups:      nil,
				Id:          "r3",
				Name:        "test3",
				Type:        api.NetworkResourceTypeSubnet,
			},
			expected: NetworkResourceModel{
				Id:          types.StringValue("r3"),
				Name:        types.StringValue("test3"),
				Description: types.StringNull(),
				Address:     types.StringValue("10.0.0.0/24"),
				Enabled:     types.BoolValue(true),
				Groups:      types.SetNull(types.StringType),
			},
		},
		{
			resource: &api.NetworkResource{
				Address:     "example.com",
				Description: valPtr("Test"),
				Enabled:     true,
				Groups: []api.GroupMinimum{
					{
						Id:             "g1",
						Name:           "not important",
						Issued:         nil,
						PeersCount:     0,
						ResourcesCount: 0,
					},
				},
				Id:   "r2",
				Name: "test2",
				Type: api.NetworkResourceTypeDomain,
			},
			expected: NetworkResourceModel{
				Id:          types.StringValue("r2"),
				Name:        types.StringValue("test2"),
				Description: types.StringValue("Test"),
				Address:     types.StringValue("example.com"),
				Enabled:     types.BoolValue(true),
				Groups:      types.SetValueMust(types.StringType, []attr.Value{types.StringValue("g1")}),
			},
		},
		{
			resource: &api.NetworkResource{
				Address:     "example.com",
				Description: valPtr("Test"),
				Enabled:     true,
				Groups: []api.GroupMinimum{
					{
						Id:             "g1",
						Name:           "not important",
						Issued:         nil,
						PeersCount:     0,
						ResourcesCount: 0,
					},
					{
						Id:             "g2",
						Name:           "doesn't matter",
						Issued:         nil,
						PeersCount:     0,
						ResourcesCount: 0,
					},
				},
				Id:   "r2",
				Name: "test2",
				Type: api.NetworkResourceTypeDomain,
			},
			expected: NetworkResourceModel{
				Id:          types.StringValue("r2"),
				Name:        types.StringValue("test2"),
				Description: types.StringValue("Test"),
				Address:     types.StringValue("example.com"),
				Enabled:     types.BoolValue(true),
				Groups:      types.SetValueMust(types.StringType, []attr.Value{types.StringValue("g1"), types.StringValue("g2")}),
			},
		},
	}

	for _, c := range cases {
		var out NetworkResourceModel
		outDiag := networkResourceAPIToTerraform(context.Background(), c.resource, &out)
		if outDiag.HasError() {
			t.Fatalf("Expected no error diagnostics, found %d errors", outDiag.ErrorsCount())
		}

		if !reflect.DeepEqual(out, c.expected) {
			t.Fatalf("Expected:\n%#v\nFound:\n%#v", c.expected, out)
		}
	}
}
