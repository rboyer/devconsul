SHELL := /bin/bash

.PHONY: all
all: init

.PHONY: gomod
gomod:
	GO111MODULE=on go mod tidy
	GO111MODULE=on go mod vendor
	GO111MODULE=on go mod download

.PHONY: init
init: docker -init-dirs tls

.PHONY: -init-dirs
-init-dirs:
	@mkdir -p cache

.PHONY: docker
docker:
	if [[ -f .env ]]; then \
		source .env ; \
	fi ; \
	docker tag $${CONSUL_IMAGE:-consul:1.4.1} local/consul-base:latest ; \
	docker build -t local/consul-envoy -f Dockerfile-envoy .

.PHONY: tls
tls:
	@mkdir -p cache/tls
	@if [[ -f .env ]]; then \
		source .env ; \
	fi ; \
	docker run \
		--rm \
		-v "$$(pwd)/cache/tls:/out" \
		-v "$$(pwd)/tls-init.sh:/bin/tls-init.sh:ro" \
		-w /out \
		-u "$$(id -u):$$(id -g)" \
		--entrypoint /bin/tls-init.sh \
		-it \
		$${CONSUL_IMAGE:-consul:1.4.1}

.PHONY: up
up:
	docker-compose up -d
	go run main.go

.PHONY: down
down:
	docker-compose down -v --remove-orphans
	rm -f cache/*.val

.PHONY: members
members:
	@./consul.sh members

.PHONY: services
services:
	@./consul.sh catalog services

.PHONY: use-dev
use-dev:
	$(info switching to dev builds)
	@if [[ -f .env ]]; then \
		sed -i '/CONSUL_IMAGE=/d' .env ; \
	fi
	@echo "CONSUL_IMAGE=consul-dev:latest" >> .env
