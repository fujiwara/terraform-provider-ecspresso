package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/fujiwara/tfstate-lookup/tfstate"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccEcspressoService_basic runs a full Create / Read / Delete
// cycle of `ecspresso_service` against a real ECS service. It requires
// an opt-in via TF_ACC=1 (set automatically by the testing framework
// when `go test` is invoked) plus the following env vars naming the
// fixture to deploy into:
//
//	ECSPRESSO_TEST_CONFIG_PATH  absolute path to an ecspresso.yml that
//	                            already points at a deployable service
//	TFSTATE_URL                 URL of the bootstrap stack's tfstate;
//	                            the test reads it to populate
//	                            tfstate_values for the resource under
//	                            test (the provider itself ignores the
//	                            tfstate plugin's scanned data — see
//	                            configureTFStatePlugin in ecspressoapi).
//
// The test is skipped when the fixture is not configured so this file
// stays compatible with `go test ./...` on a developer machine that
// has no AWS access.
func TestAccEcspressoService_basic(t *testing.T) {
	cfgPath := os.Getenv("ECSPRESSO_TEST_CONFIG_PATH")
	if cfgPath == "" {
		t.Skip("ECSPRESSO_TEST_CONFIG_PATH not set; skipping acceptance test")
	}
	tfstateURL := os.Getenv("TFSTATE_URL")
	if tfstateURL == "" {
		t.Skip("TFSTATE_URL not set; skipping acceptance test")
	}

	tfstateValuesHCL, err := loadBootstrapTFStateValues(tfstateURL,
		"output.task_execution_role_arn",
		"output.subnet_ids",
		"output.security_group_id",
	)
	if err != nil {
		t.Fatalf("failed to load bootstrap tfstate from %s: %v", tfstateURL, err)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
provider "ecspresso" {}

resource "ecspresso_service" "test" {
  config_path = %q
  tfstate_values = {
%s
  }
}
`, cfgPath, tfstateValuesHCL),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("ecspresso_service.test", "id"),
					resource.TestCheckResourceAttrSet("ecspresso_service.test", "service_arn"),
					resource.TestCheckResourceAttrSet("ecspresso_service.test", "service_name"),
					resource.TestCheckResourceAttrSet("ecspresso_service.test", "cluster_arn"),
					resource.TestCheckResourceAttrSet("ecspresso_service.test", "cluster_name"),
					resource.TestCheckResourceAttrSet("ecspresso_service.test", "last_apply_at"),
				),
			},
		},
	})
}

// loadBootstrapTFStateValues reads the bootstrap stack's tfstate from
// tfstateURL and returns an HCL fragment listing the requested keys as
// `"key" = <json-value>` entries — JSON literal syntax for strings,
// numbers, lists, bools, and objects is valid HCL, so json.Marshal'd
// values can be inlined directly.
func loadBootstrapTFStateValues(tfstateURL string, keys ...string) (string, error) {
	ctx := context.Background()
	state, err := tfstate.ReadURL(ctx, tfstateURL)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, key := range keys {
		obj, err := state.Lookup(key)
		if err != nil {
			return "", fmt.Errorf("lookup %s: %w", key, err)
		}
		v, err := json.Marshal(obj.Value)
		if err != nil {
			return "", fmt.Errorf("marshal %s: %w", key, err)
		}
		fmt.Fprintf(&b, "    %q = %s\n", key, v)
	}
	return b.String(), nil
}
