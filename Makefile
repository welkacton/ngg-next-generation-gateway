GO ?= go
NPM ?= npm
BINARY := bin/nggd
CONFIG ?= configs/ngg.yaml

.PHONY: all build backend frontend test vet clean run dev install dist

all: build

build: backend

backend:
	$(GO) build -trimpath -o $(BINARY) ./cmd/nggd

frontend:
	cd web && $(NPM) install && $(NPM) run build

test:
	$(GO) test ./... -count=1

vet:
	$(GO) vet ./...

clean:
	rm -rf bin/nggd bin/nggd.exe web/dist

run: backend
	./$(BINARY) -config $(CONFIG)

dev:
	cd web && $(NPM) run dev

install: backend frontend
	@echo "See docs/04-installation.md for systemd deployment."

dist: vet test backend frontend
	@echo "NGG Phase 1 distribution ready in bin/ and web/dist/"
