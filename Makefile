.PHONY: build clean test fmt vet install dist

BINARY := terraform-provider-ecspresso

build: $(BINARY)

$(BINARY): go.mod go.sum *.go internal/**/*.go
	go build -o $@ .

clean:
	rm -rf $(BINARY) dist/

test:
	go test -race -v ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

install:
	go install .

dist:
	goreleaser build --snapshot --clean
