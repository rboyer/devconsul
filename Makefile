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
init: -gen-init docker k8s

.PHONY: -gen-init
-gen-init: -preflight -init-dirs crypto

.PHONY: -preflight
-preflight:
	@if [[ ! -f config.hcl ]]; then \
		echo "Missing required config.hcl file" >&2 ; \
		exit 1 ; \
	fi

.PHONY: -init-dirs
-init-dirs:
	@mkdir -p cache

.PHONY: docker
docker: cache/docker.done
cache/docker.done: $(PROGRAM_NAME) config.hcl Dockerfile-envoy
	docker tag "$(shell ./$(PROGRAM_NAME) config image)" local/consul-base:latest ; \
	docker build -t local/consul-envoy -f Dockerfile-envoy .
	@touch cache/docker.done

.PHONY: crypto
crypto: cache/tls/done
cache/tls/done: $(PROGRAM_NAME) config.hcl tls-init.sh
	@mkdir -p cache/tls
	@if [[ -n "$$(./$(PROGRAM_NAME) config tls)" ]]; then \
		CONSUL_IMAGE="$$(./$(PROGRAM_NAME) config image)" ; \
		docker run \
			--rm \
			--net=none \
			-v "$$(pwd)/cache/tls:/out" \
			-v "$$(pwd)/tls-init.sh:/bin/tls-init.sh:ro" \
			-w /out \
			-e N_SERVERS_DC1="$$(./$(PROGRAM_NAME) config topologyServersDatacenter1)" \
			-e N_SERVERS_DC2="$$(./$(PROGRAM_NAME) config topologyServersDatacenter2)" \
			-e N_CLIENTS_DC1="$$(./$(PROGRAM_NAME) config topologyClientsDatacenter1)" \
			-e N_CLIENTS_DC2="$$(./$(PROGRAM_NAME) config topologyClientsDatacenter2)" \
			-u "$$(id -u):$$(id -g)" \
			--entrypoint /bin/tls-init.sh \
			$${CONSUL_IMAGE} ; \
	fi
	@touch cache/tls/done

.PHONY: k8s
k8s: cache/k8s/done
cache/k8s/done: $(PROGRAM_NAME) config.hcl scripts/k8s-rbac.sh
	@mkdir -p cache/k8s
	@if [[ -n "$$(./$(PROGRAM_NAME) config k8s)" ]]; then \
		./scripts/k8s-rbac.sh ; \
	fi
	@touch cache/k8s/done

.PHONY: gen
gen: -gen-init docker-compose.yml cache/agent-master-token.val cache/gossip-key.val
docker-compose.yml: $(PROGRAM_NAME) config.hcl
	./$(PROGRAM_NAME) gen

.PHONY: up
up: gen
	docker-compose up -d
	./$(PROGRAM_NAME) boot

.PHONY: down
down: gen
	docker-compose down -v --remove-orphans
	rm -f docker-compose.yml
	rm -f cache/*.val cache/*.hcl

.PHONY: members
members:
	@./consul.sh dc1 members

.PHONY: services
services:
	@./consul.sh dc1 catalog services
