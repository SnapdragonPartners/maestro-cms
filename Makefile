.PHONY: build test test-integration test-coverage lint fix fix-imports tidy install-lint install-goimports install-hooks clean

FAKEGCS_CONTAINER := maestro-cms-fakegcs

# Build all packages.
build: lint
	go build ./...

# Run unit tests with coverage.
# Single test: make test TESTARGS='-run TestName ./content/...'
test:
	go test -cover $(TESTARGS) ./...

# Run build-tagged integration tests against a Dockerized fake-gcs-server.
# Requires Docker. Starts the emulator, waits for readiness, runs the
# integration-tagged tests with STORAGE_EMULATOR_HOST set, then tears it down.
# Single test: make test-integration TESTARGS='-run TestGCSRoundTrip ./store/gcs/...'
test-integration:
	docker run -d --rm --name $(FAKEGCS_CONTAINER) -p 4443:4443 fsouza/fake-gcs-server -scheme http -backend memory -public-host localhost:4443 >/dev/null
	@for i in $$(seq 1 50); do curl -sf "http://localhost:4443/storage/v1/b?project=test" >/dev/null 2>&1 && break || sleep 0.2; done
	@STORAGE_EMULATOR_HOST=http://localhost:4443 go test -tags=integration $(TESTARGS) ./... ; \
		status=$$? ; docker stop $(FAKEGCS_CONTAINER) >/dev/null ; exit $$status

# Generate an HTML coverage report.
test-coverage:
	@mkdir -p coverage
	go test -coverprofile=coverage/coverage.out ./...
	go tool cover -html=coverage/coverage.out -o coverage/coverage.html
	@echo "Coverage report: coverage/coverage.html"

# Install golangci-lint if not present.
install-lint:
	@which golangci-lint > /dev/null || { \
		echo "Installing golangci-lint..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8; \
	}

# Install goimports if not present.
install-goimports:
	@which goimports > /dev/null || { \
		echo "Installing goimports..."; \
		go install golang.org/x/tools/cmd/goimports@latest; \
	}

# Run formatting and linting.
lint: install-lint
	go fmt ./...
	golangci-lint run

# Auto-fix import grouping.
fix-imports: install-goimports
	goimports -w .

# Run all automatic fixes.
fix: fix-imports
	@echo "Automatic fixes applied"

# Tidy module dependencies.
tidy:
	go mod tidy

# Install git hooks (non-fatal on read-only checkouts / CI).
install-hooks:
	@if [ -d .git ] && [ -w .git/hooks ]; then \
		cp hooks/pre-push .git/hooks/pre-push && chmod +x .git/hooks/pre-push; \
		echo "Git hooks installed"; \
	fi

# Remove build and coverage artifacts.
clean:
	rm -rf bin/ coverage/
