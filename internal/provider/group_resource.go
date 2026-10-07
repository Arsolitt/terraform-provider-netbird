// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	netbird "github.com/netbirdio/netbird/shared/management/client/rest"
	"github.com/netbirdio/netbird/shared/management/http/api"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &Group{}
var _ resource.ResourceWithImportState = &Group{}
var _ resource.ResourceWithUpgradeState = &Group{}

func NewGroup() resource.Resource {
	return &Group{}
}

// Group defines the resource implementation.
type Group struct {
	client *netbird.Client
}

// GroupModel describes the resource data model.
type GroupModel struct {
	Id        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Peers     types.List   `tfsdk:"peers"`
	Resources types.List   `tfsdk:"resources"`
	Issued    types.String `tfsdk:"issued"`
}

// GroupResourceModel describes one entry of the resources attribute: the id of a
// resource and the kind of resource it names.
type GroupResourceModel struct {
	Id   types.String `tfsdk:"id"`
	Type types.String `tfsdk:"type"`
}

// TFType returns the object type of a single group resource entry.
func (g GroupResourceModel) TFType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"id":   types.StringType,
			"type": types.StringType,
		},
	}
}

// groupModelV0 describes the resource state written while resources was still a
// flat list of ids, and only exists to decode that state during the upgrade.
type groupModelV0 struct {
	Id        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Peers     types.List   `tfsdk:"peers"`
	Resources types.List   `tfsdk:"resources"`
	Issued    types.String `tfsdk:"issued"`
}

func (r *Group) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *Group) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		Description:         "Create and assign Groups",
		MarkdownDescription: "Create and assign Groups, see [NetBird Docs](https://docs.netbird.io/how-to/manage-network-access#groups) for more information.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Group ID",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Group name identifier",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"issued": schema.StringAttribute{
				MarkdownDescription: "Group origin. Set to `jwt` to pre-provision a group that will receive members from the IdP JWT `groups` claim at login (no prior IdP login required). Set to `api` for a regular group. Defaults to `api`. Changing this value updates the group in place (no resource replacement).",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{stringvalidator.OneOf("api", "jwt")},
			},
			"peers": schema.ListAttribute{
				MarkdownDescription: "List of peers ids",
				ElementType:         types.StringType,
				Computed:            true,
				Optional:            true,
				PlanModifiers:       []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				Validators:          []validator.List{listvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
			},
			"resources": schema.ListNestedAttribute{
				MarkdownDescription: "List of network resources attached to the group. Each entry pairs a resource `id` with its `type`: `peer`, `domain`, `host`, or `subnet`.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: "Resource ID",
							Required:            true,
							Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
						},
						"type": schema.StringAttribute{
							MarkdownDescription: "Resource type",
							Required:            true,
							Validators:          []validator.String{stringvalidator.OneOf("peer", "domain", "host", "subnet")},
						},
					},
				},
			},
		},
	}
}

func (r *Group) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*netbird.Client)

	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *netbird.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.client = client
}

func groupAPIToTerraform(ctx context.Context, group *api.Group, data *GroupModel) diag.Diagnostics {
	var ret diag.Diagnostics
	data.Id = types.StringValue(group.Id)
	data.Name = types.StringValue(group.Name)
	data.Issued = types.StringPointerValue((*string)(group.Issued))
	peers := make([]string, len(group.Peers))
	for i, v := range group.Peers {
		peers[i] = v.Id
	}
	peersList, diags := types.ListValueFrom(ctx, types.StringType, peers)
	ret.Append(diags...)
	data.Peers = peersList

	resources := make([]GroupResourceModel, len(group.Resources))
	for i, res := range group.Resources {
		resources[i] = GroupResourceModel{
			Id:   types.StringValue(res.Id),
			Type: types.StringValue(string(res.Type)),
		}
	}
	resourcesList, diags := types.ListValueFrom(ctx, GroupResourceModel{}.TFType(), resources)
	ret.Append(diags...)
	data.Resources = resourcesList
	return ret
}

// groupResourcesTerraformToAPI converts the resources attribute into the API
// request shape, pairing every entry's id with its type. A null, unknown or
// empty list yields nil, which leaves the field out of the request; management
// stores that as an empty set, so it applies its default on create and clears
// the group's resources on update. An unset Optional+Computed attribute is
// unknown in the plan, and treating that as a reason to fail would reject every
// group that never mentions resources — the refreshed state carries the
// server's own pairs back, so an untouched group round-trips them.
func groupResourcesTerraformToAPI(ctx context.Context, list types.List) (*[]api.Resource, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}

	var resources []GroupResourceModel
	diags := list.ElementsAs(ctx, &resources, false)
	if diags.HasError() {
		return nil, diags
	}

	if len(resources) == 0 {
		return nil, nil
	}

	apiResources := make([]api.Resource, len(resources))
	for i, res := range resources {
		apiResources[i] = api.Resource{
			Id:   res.Id.ValueString(),
			Type: api.ResourceType(res.Type.ValueString()),
		}
	}

	return &apiResources, nil
}

// groupResourcesStateV0ToV1 converts the flat resource id list written by state
// version 0 into the nested list the current schema expects. A bare id carries
// no type, and the API requires one per entry, so the ids cannot be carried
// over: a non-empty list upgrades to null and the next read refills it with
// id/type pairs. Null and empty lists keep their shape so that the upgrade
// cannot turn a known-empty attribute into a diff.
func groupResourcesStateV0ToV1(v0 types.List) types.List {
	t := GroupResourceModel{}.TFType()

	if !v0.IsNull() && !v0.IsUnknown() && len(v0.Elements()) == 0 {
		return types.ListValueMust(t, []attr.Value{})
	}

	return types.ListNull(t)
}

func (r *Group) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data GroupModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resources, diags := groupResourcesTerraformToAPI(ctx, data.Resources)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	groupReq := api.GroupRequest{
		Name:      data.Name.ValueString(),
		Peers:     stringListDefaultPointer(ctx, data.Peers, nil),
		Resources: resources,
		Issued:    groupRequestIssued(data.Issued),
	}

	group, err := r.client.Groups.Create(ctx, groupReq)
	if err != nil {
		resp.Diagnostics.AddError("Error creating group", err.Error())
		return
	}

	resp.Diagnostics.Append(groupAPIToTerraform(ctx, group, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *Group) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data GroupModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	group, err := r.client.Groups.Get(ctx, data.Id.ValueString())
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			resp.State.RemoveResource(ctx)
			return
		} else {
			resp.Diagnostics.AddError("Error getting Group", err.Error())
		}
		return
	}

	resp.Diagnostics.Append(groupAPIToTerraform(ctx, group, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *Group) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data GroupModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if data.Id.ValueString() == "" {
		r.Create(ctx, resource.CreateRequest{Config: req.Config, Plan: req.Plan, ProviderMeta: req.ProviderMeta}, (*resource.CreateResponse)(resp))
		return
	}

	resources, diags := groupResourcesTerraformToAPI(ctx, data.Resources)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	groupReq := api.GroupRequest{
		Name:      data.Name.ValueString(),
		Peers:     stringListDefaultPointer(ctx, data.Peers, nil),
		Resources: resources,
		Issued:    groupRequestIssued(data.Issued),
	}

	group, err := r.client.Groups.Update(ctx, data.Id.ValueString(), groupReq)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Group", err.Error())
		return
	}

	resp.Diagnostics.Append(groupAPIToTerraform(ctx, group, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *Group) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data GroupModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Groups.Delete(ctx, data.Id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Group", err.Error())
	}
}

func (r *Group) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// UpgradeState upgrades state written before version 1, where resources was a
// flat list of ids.
func (r *Group) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: groupSchemaV0(),
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior groupModelV0
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)

				if resp.Diagnostics.HasError() {
					return
				}

				resp.Diagnostics.Append(resp.State.Set(ctx, GroupModel{
					Id:        prior.Id,
					Name:      prior.Name,
					Peers:     prior.Peers,
					Resources: groupResourcesStateV0ToV1(prior.Resources),
					Issued:    prior.Issued,
				})...)
			},
		},
	}
}

// groupSchemaV0 is the schema state version 0 was written against, kept only so
// that the upgrader can decode it. Its resources attribute is the flat list of
// ids the current schema replaced.
func groupSchemaV0() *schema.Schema {
	return &schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"name": schema.StringAttribute{
				Required: true,
			},
			"issued": schema.StringAttribute{
				Optional: true,
				Computed: true,
			},
			"peers": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
			},
			"resources": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
			},
		},
	}
}

// groupRequestIssued converts the Terraform `issued` attribute into the API enum
// expected by the group requests. It returns nil for an unset value and for an
// origin the request enum cannot express, such as the "integration" origin of a
// group provisioned through an integration. Omitting the field makes the server
// apply its own default on create and keep the existing origin on update, while
// sending a value outside the enum is rejected.
func groupRequestIssued(v types.String) *api.GroupRequestIssued {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}

	issued := api.GroupRequestIssued(v.ValueString())
	if !issued.Valid() {
		return nil
	}

	return &issued
}
