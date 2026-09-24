.PHONY: build test run-server run-scan clean

export PATH := /usr/local/go/bin:$(PATH)

build:
	go build -o bin/tfdriftctl ./cmd/driftctl
	go build -o bin/tfdrift-server ./cmd/drift-server

test:
	go test ./...

run-server: build
	./bin/tfdrift-server -config configs/driftctl.yaml

run-scan: build
	./bin/tfdriftctl scan --state testdata/state/sample.tfstate --provider aws --skip-cloud

clean:
	rm -rf bin/
