SHELL := /bin/bash

PROGRAM_NAME := devconsul

.PHONY: all
all: bin init

.PHONY: bin
bin: $(PROGRAM_NAME)
$(PROGRAM_NAME): *.go cachestore/*.go consulfunc/*.go go.mod go.sum
	$(info rebuilding binary...)
	@go build

.PHONY: init
init: docker k8s

.PHONY: force-docker
force-docker:
	@rm -f cache/docker.done
	$(MAKE) docker

.PHONY: docker
docker: cache/docker.done
cache/docker.done: $(PROGRAM_NAME) config.hcl Dockerfile-envoy
	docker tag "$(shell ./$(PROGRAM_NAME) config image)" local/consul-base:latest ; \
	docker build -t local/consul-envoy -f Dockerfile-envoy .
	@touch cache/docker.done

.PHONY: k8s
k8s: cache/k8s/done
cache/k8s/done: $(PROGRAM_NAME) config.hcl scripts/k8s-rbac.sh
	@mkdir -p cache/k8s
	@if [[ -n "$$(./$(PROGRAM_NAME) config k8s)" ]]; then \
		./scripts/k8s-rbac.sh ; \
	fi
	@touch cache/k8s/done

.PHONY: gen
gen: docker-compose.yml cache/agent-master-token.val cache/gossip-key.val
docker-compose.yml: $(PROGRAM_NAME) config.hcl cache/docker.done
	./$(PROGRAM_NAME) gen

.PHONY: up
up: gen
	docker-compose up -d
	./$(PROGRAM_NAME) boot

.PHONY: down
down: gen
	docker-compose down -v --remove-orphans
	rm -f docker-compose.yml
	rm -f cache/*.val cache/*.hcl cache/docker.done

.PHONY: restart
restart: gen
	docker-compose down
	docker-compose up -d
	./$(PROGRAM_NAME) boot

.PHONY: members
members:
	@./consul.sh dc1 members

.PHONY: services
services:
	@./consul.sh dc1 catalog services

.PHONY: configs
configs:
	@for kind in service-router service-splitter service-resolver service-defaults proxy-defaults ; do \
	    names=$$(./consul.sh dc1 config list -kind $$kind | sort) ; \
		for name in $$names; do \
			echo "$$kind/$$name" ; \
		done ; \
	done
