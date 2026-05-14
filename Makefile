.PHONY: test check-lines build validate demo fmt tidy clean

BIN ?= bin/kube-insight
CONFIG ?= config/kube-insight.example.yaml

test: check-lines
	go test ./...

check-lines:
	@overs="$$(wc -l $$(rg --files -g '*.go' cmd internal) | awk '$$1 > 800 && $$2 != "total" {print}')"; \
	if [ -n "$$overs" ]; then \
		echo "Go files exceed 800 lines:"; \
		echo "$$overs"; \
		exit 1; \
	fi

build:
	mkdir -p $(dir $(BIN))
	go build -o $(BIN) ./cmd/kube-insight

validate:
	go run ./cmd/kube-insight config validate --file $(CONFIG)
	go run ./cmd/kube-insight dev validate poc --fixtures testdata/fixtures/kube --output testdata/generated/poc-validation --db testdata/generated/poc-validation/kube-insight.db --clusters 1 --copies 2 --query-runs 3

demo:
	./scripts/poc-demo.sh

fmt:
	gofmt -w cmd internal

tidy:
	go mod tidy

clean:
	rm -rf bin testdata/generated
