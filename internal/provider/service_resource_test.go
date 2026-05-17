package provider

import (
	"reflect"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAttrValueToGo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		v    attr.Value
		want any
	}{
		{"string", types.DynamicValue(types.StringValue("arn:aws:iam::123:role/x")), "arn:aws:iam::123:role/x"},
		{"bool true", types.DynamicValue(types.BoolValue(true)), true},
		{"bool false", types.DynamicValue(types.BoolValue(false)), false},
		{"int64", types.DynamicValue(types.Int64Value(123)), int64(123)},
		{"null dynamic", types.DynamicNull(), nil},
		{
			name: "list of strings",
			v: types.DynamicValue(
				types.ListValueMust(types.StringType, []attr.Value{
					types.StringValue("a"),
					types.StringValue("b"),
				}),
			),
			want: []any{"a", "b"},
		},
		{
			name: "object with string and bool",
			v: types.DynamicValue(
				types.ObjectValueMust(
					map[string]attr.Type{
						"name":    types.StringType,
						"enabled": types.BoolType,
					},
					map[string]attr.Value{
						"name":    types.StringValue("app"),
						"enabled": types.BoolValue(true),
					},
				),
			),
			want: map[string]any{"name": "app", "enabled": true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := attrValueToGo(tc.v)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("attrValueToGo(%v) = %#v, want %#v", tc.v, got, tc.want)
			}
		})
	}
}

func TestNowTimestampParses(t *testing.T) {
	t.Parallel()
	got := nowTimestamp()
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Errorf("nowTimestamp() = %q, not RFC3339: %s", got, err)
	}
}

func TestUpdateNeedsDeploy(t *testing.T) {
	t.Parallel()

	base := func() serviceResourceModel {
		return serviceResourceModel{
			ConfigPath: types.StringValue("ecspresso.yml"),
			TFStateValues: types.DynamicValue(
				types.ObjectValueMust(
					map[string]attr.Type{"k": types.StringType},
					map[string]attr.Value{"k": types.StringValue("v")},
				),
			),
			TFStateFuncPrefix: types.StringValue(""),
			DestroyAction:     types.StringValue("delete"),
		}
	}

	tests := []struct {
		name string
		mut  func(*serviceResourceModel)
		want bool
	}{
		{
			name: "no change",
			mut:  func(_ *serviceResourceModel) {},
			want: false,
		},
		{
			name: "destroy_action only",
			mut: func(m *serviceResourceModel) {
				m.DestroyAction = types.StringValue("ignore")
			},
			want: false,
		},
		{
			name: "tfstate_values changed",
			mut: func(m *serviceResourceModel) {
				m.TFStateValues = types.DynamicValue(
					types.ObjectValueMust(
						map[string]attr.Type{"k": types.StringType},
						map[string]attr.Value{"k": types.StringValue("v2")},
					),
				)
			},
			want: true,
		},
		{
			name: "tfstate_func_prefix changed",
			mut: func(m *serviceResourceModel) {
				m.TFStateFuncPrefix = types.StringValue("alt_")
			},
			want: true,
		},
		{
			name: "destroy_action and tfstate_values both changed",
			mut: func(m *serviceResourceModel) {
				m.DestroyAction = types.StringValue("ignore")
				m.TFStateValues = types.DynamicValue(
					types.ObjectValueMust(
						map[string]attr.Type{"k": types.StringType},
						map[string]attr.Value{"k": types.StringValue("v2")},
					),
				)
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := base()
			plan := base()
			tc.mut(&plan)
			if got := updateNeedsDeploy(plan, state); got != tc.want {
				t.Errorf("updateNeedsDeploy() = %v, want %v", got, tc.want)
			}
		})
	}
}
