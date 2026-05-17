.PHONY: build clean test acc-test fmt vet install dist docs

BINARY := terraform-provider-ecspresso

# Exclude tfstate-lookup backends that ECS users effectively never use.
# Keeps S3 and Terraform Enterprise / Terraform Cloud (HTTP) backends.
BUILD_TAGS := no_gcs,no_azurerm

build: $(BINARY)

$(BINARY): go.mod go.sum *.go internal/**/*.go
	go build -tags "$(BUILD_TAGS)" -o $@ .

clean:
	rm -rf $(BINARY) dist/

test:
	go test -race -v ./...

# Acceptance tests hit real AWS via a real ECS service. They require:
#   AWS_REGION (or AWS_DEFAULT_REGION) plus working AWS credentials
#   ECSPRESSO_TEST_CONFIG_PATH — absolute path to an ecspresso.yml
#                                 fixture (see service_resource_acc_test.go)
# Tests are skipped when the fixture env var is unset, so this target
# is safe to invoke without one configured.
acc-test:
	TF_ACC=1 go test -tags "$(BUILD_TAGS)" -v -timeout 30m ./internal/provider/

fmt:
	go fmt ./...

vet:
	go vet ./...

install:
	go install -tags "$(BUILD_TAGS)" .

dist:
	goreleaser build --snapshot --clean

# Re-generate the Terraform Registry documentation under docs/ from the
# Plugin Framework schema. Run this after changing resource schemas.
# Requires `tfplugindocs` (https://github.com/hashicorp/terraform-plugin-docs).
docs:
	tfplugindocs generate --provider-name ecspresso
