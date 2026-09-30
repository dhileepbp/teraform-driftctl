.PHONY: build test run-scan clean

export PATH := /usr/local/go/bin:$(PATH)

build:
	go build -o bin/tfdriftctl ./cmd/driftctl

test:
	go test ./...

run-scan: build
	./bin/tfdriftctl scan --state testdata/state/azure-sample.tfstate --provider azure --skip-cloud

clean:
	rm -rf bin/
