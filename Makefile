.PHONY: test check-lines web-deps web-build web-lint build build-default build-chdb build-local-variants build-chdb-image prepare-chdb-runtime release-chdb-check validate chdb-build-check chdb-smoke clickhouse-up clickhouse-down clickhouse-smoke clickhouse-benchmark clickhouse-live-profile clickhouse-api-smoke storage-mode-benchmark mcp-sql-first-smoke release-artifact-smoke live-service-vs-kubectl clickhouse-status clickhouse-repair-plan clickhouse-cleanup-repair-artifacts clickhouse-clean-system-logs clickhouse-serve-dev dev-compose-up dev-compose-up-detached dev-compose-down dev-compose-logs dev-compose-ps open-source-check demo fmt tidy clean

-include .env

BIN ?= bin/kube-insight
GO_TEST_PACKAGES ?= ./cmd/... ./internal/... ./web
WEB_DIR ?= web
WEB_NODE_MODULES_STAMP ?= $(WEB_DIR)/node_modules/.package-lock.json
CHDB_BIN ?= bin/kube-insight-chdb
CHDB_VERSION ?= 3.7.2
CHDB_LIB ?= /usr/local/lib/libchdb.so
CHDB_RUNTIME_DIR ?= build/chdb-runtime
CHDB_IMAGE ?= kube-insight-chdb:local
CHDB_IMAGE_CONTEXT ?= dist/chdb-image
CONFIG ?= config/kube-insight.example.yaml
CLICKHOUSE_IMAGE ?= clickhouse/clickhouse-server:25.3
CLICKHOUSE_CONTAINER ?= kube-insight-clickhouse-dev
CLICKHOUSE_HTTP_PORT ?= 8123
CLICKHOUSE_SMOKE_CONTAINER ?= kube-insight-clickhouse-smoke
CLICKHOUSE_SMOKE_HTTP_PORT ?= 18123
CLICKHOUSE_DATABASE ?= kube_insight
CLICKHOUSE_USER ?= default
CLICKHOUSE_PASSWORD ?= kube-insight
CLICKHOUSE_ENDPOINT ?= http://127.0.0.1:$(CLICKHOUSE_HTTP_PORT)
CLICKHOUSE_DEV_CONFIG ?= config/kube-insight.clickhouse.example.yaml
CLICKHOUSE_DEV_RESOURCES ?= pods services endpointslices.discovery.k8s.io
CLICKHOUSE_DEV_SERVE_FLAGS ?= --api --metrics
COMPOSE_DEV_LOCAL := $(wildcard compose.dev.local.yaml)
COMPOSE_DEV_FILES := -f compose.dev.yaml $(if $(COMPOSE_DEV_LOCAL),-f $(COMPOSE_DEV_LOCAL))

test: web-build check-lines
	go test $(GO_TEST_PACKAGES)

check-lines:
	@overs="$$(wc -l $$(rg --files -g '*.go' cmd internal web) | awk '$$1 > 800 && $$2 != "total" {print}')"; \
	if [ -n "$$overs" ]; then \
		echo "Go files exceed 800 lines:"; \
		echo "$$overs"; \
		exit 1; \
	fi

$(WEB_NODE_MODULES_STAMP): $(WEB_DIR)/package.json $(WEB_DIR)/package-lock.json
	npm --prefix $(WEB_DIR) ci
	touch $(WEB_NODE_MODULES_STAMP)

web-deps: $(WEB_NODE_MODULES_STAMP)

web-build: web-deps
	npm --prefix $(WEB_DIR) run build

web-lint: web-deps
	npm --prefix $(WEB_DIR) run lint

build: web-build
	mkdir -p $(dir $(BIN))
	go build -o $(BIN) ./cmd/kube-insight

build-default: build

build-chdb: web-build
	mkdir -p $(dir $(CHDB_BIN))
	go build -tags chdb -o $(CHDB_BIN) ./cmd/kube-insight

build-local-variants: build-default build-chdb

build-chdb-image: build-chdb
	test -f $(CHDB_LIB) || (echo "libchdb.so not found at $(CHDB_LIB); set CHDB_LIB" >&2; exit 1)
	mkdir -p $(CHDB_IMAGE_CONTEXT)/linux/amd64 $(CHDB_IMAGE_CONTEXT)/build/chdb-runtime
	cp $(CHDB_BIN) $(CHDB_IMAGE_CONTEXT)/linux/amd64/kube-insight
	cp $(CHDB_LIB) $(CHDB_IMAGE_CONTEXT)/build/chdb-runtime/libchdb-linux-amd64.so
	docker build --build-arg TARGETOS=linux --build-arg TARGETARCH=amd64 -f docker/chdb.Dockerfile -t $(CHDB_IMAGE) $(CHDB_IMAGE_CONTEXT)

prepare-chdb-runtime:
	mkdir -p $(CHDB_RUNTIME_DIR)
	rm -f $(CHDB_RUNTIME_DIR)/libchdb.so
	@download_chdb_runtime() { \
		asset="$$1"; \
		dest="$$2"; \
		tmp="$(CHDB_RUNTIME_DIR)/$${asset}.tar.gz"; \
		mkdir -p "$$dest"; \
		curl -fsSL -o "$$tmp" "https://github.com/chdb-io/chdb/releases/download/v$(CHDB_VERSION)/$${asset}.tar.gz"; \
		tar -xzf "$$tmp" -C "$$dest" libchdb.so; \
		rm -f "$$tmp"; \
		test -s "$$dest/libchdb.so"; \
	}; \
	download_chdb_runtime linux-x86_64-libchdb '$(CHDB_RUNTIME_DIR)/linux-amd64'; \
	download_chdb_runtime linux-aarch64-libchdb '$(CHDB_RUNTIME_DIR)/linux-arm64'; \
	download_chdb_runtime macos-x86_64-libchdb '$(CHDB_RUNTIME_DIR)/darwin-amd64'; \
	download_chdb_runtime macos-arm64-libchdb '$(CHDB_RUNTIME_DIR)/darwin-arm64'; \
	cp '$(CHDB_RUNTIME_DIR)/linux-amd64/libchdb.so' '$(CHDB_RUNTIME_DIR)/libchdb-linux-amd64.so'; \
	cp '$(CHDB_RUNTIME_DIR)/linux-arm64/libchdb.so' '$(CHDB_RUNTIME_DIR)/libchdb-linux-arm64.so'

release-chdb-check: prepare-chdb-runtime
	command -v goreleaser >/dev/null || (echo "goreleaser is required for release-chdb-check" >&2; exit 1)
	goreleaser check --config .goreleaser.yaml

validate: web-build
	go run ./cmd/kube-insight config validate --file $(CONFIG)
	go run ./cmd/kube-insight dev validate poc --fixtures testdata/fixtures/kube --output testdata/generated/poc-validation --db testdata/generated/poc-validation/kube-insight.db --clusters 1 --copies 2 --query-runs 3

chdb-build-check: web-build
	go test -c -tags chdb ./internal/storage/chdb -o /tmp/kube-insight-chdb-store.test
	go test -c -tags chdb ./internal/cli -o /tmp/kube-insight-chdb-cli.test
	go build -tags chdb -o /tmp/kube-insight-chdb ./cmd/kube-insight

chdb-smoke:
	./scripts/chdb-smoke.sh

clickhouse-up:
	@if docker ps -a --format '{{.Names}}' | grep -qx '$(CLICKHOUSE_CONTAINER)'; then \
		docker start '$(CLICKHOUSE_CONTAINER)' >/dev/null; \
	else \
		docker run -d \
			--name '$(CLICKHOUSE_CONTAINER)' \
			-e CLICKHOUSE_DB='$(CLICKHOUSE_DATABASE)' \
			-e CLICKHOUSE_USER='$(CLICKHOUSE_USER)' \
			-e CLICKHOUSE_PASSWORD='$(CLICKHOUSE_PASSWORD)' \
			-e CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 \
			-p '$(CLICKHOUSE_HTTP_PORT):8123' \
			'$(CLICKHOUSE_IMAGE)' >/dev/null; \
	fi
	@for i in $$(seq 1 60); do \
		if curl -fsS '$(CLICKHOUSE_ENDPOINT)/ping' >/dev/null 2>&1; then \
			echo 'ClickHouse is ready at $(CLICKHOUSE_ENDPOINT) (credentials from .env)'; \
			exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo 'ClickHouse did not become ready at $(CLICKHOUSE_ENDPOINT)' >&2; \
	docker logs '$(CLICKHOUSE_CONTAINER)' >&2 || true; \
	exit 1

clickhouse-down:
	docker rm -f '$(CLICKHOUSE_CONTAINER)' >/dev/null 2>&1 || true

clickhouse-smoke: build
	@CLICKHOUSE_CONTAINER='$(CLICKHOUSE_SMOKE_CONTAINER)' \
	CLICKHOUSE_HTTP_PORT='$(CLICKHOUSE_SMOKE_HTTP_PORT)' \
	CLICKHOUSE_DATABASE='$(CLICKHOUSE_DATABASE)' \
	CLICKHOUSE_USER='$(CLICKHOUSE_USER)' \
	CLICKHOUSE_PASSWORD='$(CLICKHOUSE_PASSWORD)' \
	./scripts/clickhouse-smoke.sh

clickhouse-benchmark: build
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	CLICKHOUSE_HTTP_PORT='$(CLICKHOUSE_HTTP_PORT)' \
	CLICKHOUSE_DATABASE='$(CLICKHOUSE_DATABASE)' \
	CLICKHOUSE_USER='$(CLICKHOUSE_USER)' \
	CLICKHOUSE_PASSWORD='$(CLICKHOUSE_PASSWORD)' \
	./scripts/clickhouse-benchmark.sh

clickhouse-live-profile: build
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	CLICKHOUSE_HTTP_PORT='$(CLICKHOUSE_HTTP_PORT)' \
	CLICKHOUSE_DATABASE='$(CLICKHOUSE_DATABASE)' \
	CLICKHOUSE_USER='$(CLICKHOUSE_USER)' \
	CLICKHOUSE_PASSWORD='$(CLICKHOUSE_PASSWORD)' \
	./scripts/clickhouse-live-profile.sh

clickhouse-api-smoke: build
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	CLICKHOUSE_HTTP_PORT='$(CLICKHOUSE_HTTP_PORT)' \
	CLICKHOUSE_DATABASE='$(CLICKHOUSE_DATABASE)' \
	CLICKHOUSE_USER='$(CLICKHOUSE_USER)' \
	CLICKHOUSE_PASSWORD='$(CLICKHOUSE_PASSWORD)' \
	./scripts/clickhouse-api-smoke.sh

storage-mode-benchmark: build
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	CLICKHOUSE_HTTP_PORT='$(CLICKHOUSE_HTTP_PORT)' \
	CLICKHOUSE_USER='$(CLICKHOUSE_USER)' \
	CLICKHOUSE_PASSWORD='$(CLICKHOUSE_PASSWORD)' \
	./scripts/storage-mode-benchmark.sh


mcp-sql-first-smoke: build
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	./scripts/mcp-sql-first-smoke.sh

release-artifact-smoke:
	./scripts/release-artifact-smoke.sh

live-service-vs-kubectl: build
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	CLICKHOUSE_HTTP_PORT='$(CLICKHOUSE_HTTP_PORT)' \
	CLICKHOUSE_DATABASE='$(CLICKHOUSE_DATABASE)' \
	CLICKHOUSE_USER='$(CLICKHOUSE_USER)' \
	CLICKHOUSE_PASSWORD='$(CLICKHOUSE_PASSWORD)' \
	./scripts/live-service-vs-kubectl.sh

clickhouse-status: build
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	$(BIN) db clickhouse status \
		--config '$(CLICKHOUSE_DEV_CONFIG)' \
		--database '$(CLICKHOUSE_DATABASE)'

clickhouse-repair-plan: build
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	$(BIN) db clickhouse maintenance repair-ingestion-offsets \
		--config '$(CLICKHOUSE_DEV_CONFIG)' \
		--database '$(CLICKHOUSE_DATABASE)'

clickhouse-cleanup-repair-artifacts: build
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	$(BIN) db clickhouse maintenance cleanup-repair-artifacts \
		--config '$(CLICKHOUSE_DEV_CONFIG)' \
		--database '$(CLICKHOUSE_DATABASE)'

clickhouse-clean-system-logs:
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	docker exec '$(CLICKHOUSE_CONTAINER)' clickhouse-client \
		--user "$${CLICKHOUSE_USER:-$(CLICKHOUSE_USER)}" \
		--password "$${CLICKHOUSE_PASSWORD:-$(CLICKHOUSE_PASSWORD)}" \
		--multiquery \
		--query "SYSTEM FLUSH LOGS; TRUNCATE TABLE IF EXISTS system.query_log; TRUNCATE TABLE IF EXISTS system.text_log; TRUNCATE TABLE IF EXISTS system.trace_log; TRUNCATE TABLE IF EXISTS system.part_log; TRUNCATE TABLE IF EXISTS system.metric_log; TRUNCATE TABLE IF EXISTS system.asynchronous_metric_log; TRUNCATE TABLE IF EXISTS system.latency_log; TRUNCATE TABLE IF EXISTS system.processors_profile_log; TRUNCATE TABLE IF EXISTS system.asynchronous_insert_log;"

clickhouse-serve-dev: clickhouse-up build
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	$(BIN) serve \
		--config '$(CLICKHOUSE_DEV_CONFIG)' \
		--watch $(CLICKHOUSE_DEV_RESOURCES) \
		$(CLICKHOUSE_DEV_SERVE_FLAGS)

dev-compose-up:
	docker compose $(COMPOSE_DEV_FILES) up --build

dev-compose-up-detached:
	docker compose $(COMPOSE_DEV_FILES) up --build -d

dev-compose-down:
	docker compose $(COMPOSE_DEV_FILES) down

dev-compose-logs:
	docker compose $(COMPOSE_DEV_FILES) logs -f

dev-compose-ps:
	docker compose $(COMPOSE_DEV_FILES) ps

open-source-check:
	./scripts/open-source-check.sh

demo:
	./scripts/poc-demo.sh

fmt:
	gofmt -w cmd internal

tidy:
	go mod tidy

clean:
	rm -rf bin testdata/generated
