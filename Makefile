.PHONY: build clean test fmt vet install dist

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

fmt:
	go fmt ./...

vet:
	go vet ./...

install:
	go install -tags "$(BUILD_TAGS)" .

dist:
	goreleaser build --snapshot --clean
