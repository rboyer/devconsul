SHELL := /bin/bash

PROGRAM_NAME := devconsul

.PHONY: all
all: install

.PHONY: install
install: $(GOPATH)/bin/$(PROGRAM_NAME)
$(GOPATH)/bin/$(PROGRAM_NAME): *.go cachestore/*.go consulfunc/*.go config/*.go infra/*.go util/*.go go.mod go.sum
	$(info rebuilding binary...)
	@go install

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test ./...

.PHONY: lint
lint:
	golangci-lint run

.PHONY: siege
siege: install
	$(info This is an example of using the siege tool to traverse an upstream boundary)
	siege -d 0.5s -c 5 -t 30s 'http://10.0.1.21:8080/?proxy=1'

.PHONY: help
help:
	$(info available make targets)
	$(info ----------------------)
	@grep "^[a-z0-9-][a-z0-9.-]*:" Makefile  | cut -d':' -f1 | sort
