SHELL := /bin/bash

PROGRAM_NAME := devconsul

.PHONY: all
all: install

.PHONY: install
install: $(GOPATH)/bin/$(PROGRAM_NAME)
$(GOPATH)/bin/$(PROGRAM_NAME): *.go cachestore/*.go consulfunc/*.go go.mod go.sum
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

.PHONY: grpc-check
grpc-check: install
	@for s in $$($(PROGRAM_NAME) config | jq .datacenters[] -r); do \
		for d in $$($(PROGRAM_NAME) config | jq .datacenters[] -r); do \
			echo "=== $$s -> $$d ===" ; \
			./rootcurl.sh $$s 'v1/health/service/consul?cached&dc='$$d | wc ; \
		done; \
	done
