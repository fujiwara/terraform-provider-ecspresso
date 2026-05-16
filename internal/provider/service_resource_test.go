package provider

import (
	"reflect"
	"testing"

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
