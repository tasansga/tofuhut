DIST_DIR=dist
TOFUHUT_DIR=$(DIST_DIR)/tofuhut

default: all

all: lint build test

$(DIST_DIR):
	mkdir -p $(DIST_DIR)

build: | $(DIST_DIR)
	go build -o "$(TOFUHUT_DIR)" .

lint:
	gofmt -w .
	golangci-lint run ./...

test:
	go test ./...

tidy:
	go mod tidy

clean:
	go clean
	rm -f "$(TOFUHUT_DIR)"

.PHONY: all build lint test tidy clean
