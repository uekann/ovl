GO ?= go
PACKAGES ?= ./...
ACTIONLINT_VERSION ?= v1.7.12
STATICCHECK_VERSION ?= v0.7.0

.PHONY: build check fmt format-check lint mod-check test test-race

fmt:
	$(GO) fmt $(PACKAGES)

format-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		printf '%s\n' 'Go files need formatting:' "$$unformatted"; \
		exit 1; \
	fi

mod-check:
	$(GO) mod tidy -diff

lint:
	$(GO) vet $(PACKAGES)
	$(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) $(PACKAGES)
	$(GO) run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

build:
	$(GO) build -trimpath $(PACKAGES)

test:
	$(GO) test -shuffle=on -count=1 $(PACKAGES)

test-race:
	$(GO) test -race -count=1 $(PACKAGES)

check: format-check mod-check lint build test test-race
