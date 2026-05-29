package provider

import (
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTFStateOverridesFromPlan(t *testing.T) {
	t.Parallel()

	t.Run("json object is decoded", func(t *testing.T) {
		var diags diag.Diagnostics
		m := serviceResourceModel{
			TFStateValues: types.StringValue(`{"aws_ecs_cluster.main":{"name":"c"},"n":2,"b":true}`),
		}
		got := tfstateOverridesFromPlan(m, &diags)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		cluster, ok := got["aws_ecs_cluster.main"].(map[string]any)
		if !ok || cluster["name"] != "c" {
			t.Fatalf("aws_ecs_cluster.main not decoded: %#v", got)
		}
		if got["n"] != float64(2) { // JSON numbers decode as float64
			t.Errorf("n = %#v, want float64(2)", got["n"])
		}
		if got["b"] != true {
			t.Errorf("b = %#v, want true", got["b"])
		}
	})

	t.Run("invalid json errors", func(t *testing.T) {
		var diags diag.Diagnostics
		m := serviceResourceModel{TFStateValues: types.StringValue("not json")}
		got := tfstateOverridesFromPlan(m, &diags)
		if !diags.HasError() {
			t.Fatalf("expected an error diagnostic for invalid JSON")
		}
		if got != nil {
			t.Errorf("want nil on error, got %#v", got)
		}
	})

	t.Run("null yields nil", func(t *testing.T) {
		var diags diag.Diagnostics
		m := serviceResourceModel{TFStateValues: types.StringNull()}
		if got := tfstateOverridesFromPlan(m, &diags); got != nil {
			t.Errorf("want nil, got %#v", got)
		}
	})
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
			ConfigPath:        types.StringValue("ecspresso.yml"),
			TFStateValues:     types.StringValue(`{"k":"v"}`),
			TFStateFuncPrefix: types.StringValue(""),
			DestroyAction:     types.StringValue("delete"),
		}
	}

	tests := []struct {
		name string
		mut  func(*serviceResourceModel)
		want bool
	}{
		{"no change", func(_ *serviceResourceModel) {}, false},
		{"destroy_action only", func(m *serviceResourceModel) {
			m.DestroyAction = types.StringValue("ignore")
		}, false},
		{"tfstate_values changed", func(m *serviceResourceModel) {
			m.TFStateValues = types.StringValue(`{"k":"v2"}`)
		}, true},
		{"tfstate_func_prefix changed", func(m *serviceResourceModel) {
			m.TFStateFuncPrefix = types.StringValue("alt_")
		}, true},
		{"destroy_action and tfstate_values both changed", func(m *serviceResourceModel) {
			m.DestroyAction = types.StringValue("ignore")
			m.TFStateValues = types.StringValue(`{"k":"v2"}`)
		}, true},
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
