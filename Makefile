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

LINK_MODE := $(shell $(PROGRAM_NAME) config | jq -r '.linkMode')

.PHONY: checkmesh
checkmesh: install
	@for name in $$($(PROGRAM_NAME) config | jq -r '.localAddrs | keys | .[]'); do \
		if [[ "$$name" = *client* ]]; then \
			ip="$$($(PROGRAM_NAME) config | jq -r ".localAddrs[\"$${name}\"]")" ; \
			echo "======" ; \
			echo "client: $$name" ; \
			echo "ip:     $$ip" ; \
			curl -sL "$${ip}:8080" | jq . > /tmp/out.json ; \
			echo "app:    $$(</tmp/out.json jq -r .name)" ; \
			echo "last ping: $$(</tmp/out.json jq '.pings[0]')" ; \
			echo "last pong: $$(</tmp/out.json jq '.pongs[0]')" ; \
		fi \
	done

.PHONY: help
help:
	$(info available make targets)
	$(info ----------------------)
	@grep "^[a-z0-9-][a-z0-9.-]*:" Makefile  | cut -d':' -f1 | sort
