package provider

import (
	"fmt"
	"os"
	"testing"

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
//
// The test is skipped when the fixture is not configured so this file
// stays compatible with `go test ./...` on a developer machine that
// has no AWS access.
func TestAccEcspressoService_basic(t *testing.T) {
	cfgPath := os.Getenv("ECSPRESSO_TEST_CONFIG_PATH")
	if cfgPath == "" {
		t.Skip("ECSPRESSO_TEST_CONFIG_PATH not set; skipping acceptance test")
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
}
`, cfgPath),
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
