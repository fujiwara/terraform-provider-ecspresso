package provider

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// testAccProtoV6ProviderFactories is the provider factory used by every
// acceptance test in this package. It boots an in-process instance of
// the provider over the Plugin Framework protocol v6 endpoint, so
// `terraform-plugin-testing` does not have to fetch a binary from the
// Registry.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"ecspresso": providerserver.NewProtocol6WithError(New("acctest")()),
}

// testAccPreCheck enforces preconditions shared across acceptance
// tests: the test must be opted in (the testing framework already
// checks TF_ACC) and the AWS SDK needs a region. We let resource-
// specific checks gate their own per-test fixtures (cluster name,
// config path, …) directly in the test bodies.
func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("AWS_REGION") == "" && os.Getenv("AWS_DEFAULT_REGION") == "" {
		t.Fatal("AWS_REGION or AWS_DEFAULT_REGION must be set for acceptance tests")
	}
}
