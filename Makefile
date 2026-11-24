.PHONY: build run dev clean install build-static release test

# Build everything with embedded assets
build: build-frontend build-backend

# Build frontend (typechecks, then bundles)
build-frontend:
	cd frontend && npm ci && npm run build

# Build backend with embedded assets
build-backend: build-frontend
	rm -rf backend/static
	cp -r frontend/dist backend/static
	cd backend && CGO_ENABLED=0 go build -ldflags="-w -s" -o ../labmgr .

# Release binary: a new, versioned file every time, never overwriting one that may be
# running. If this tree is on a shared filesystem (NFS) and another host runs a binary from it, it would
# crash (SIGBUS) if the file were rewritten under it.
RELEASE := releases/labmgr-$(shell git describe --always --dirty)-linux-amd64

release: build-frontend
	rm -rf backend/static
	cp -r frontend/dist backend/static
	@mkdir -p releases
	@if [ -e $(RELEASE) ]; then echo "$(RELEASE) already exists; not overwriting"; exit 1; fi
	cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o ../$(RELEASE).tmp .
	mv $(RELEASE).tmp $(RELEASE)
	@echo "Built $(RELEASE)"

build-static: release

# Backend tests (fake hardware; nothing touches real machines) and frontend typecheck
test:
	cd backend && go vet ./... && go test -race ./...
	cd frontend && npm run typecheck

# Run in development mode (no embedded assets)
dev:
	@echo "Starting Lab Manager in development mode..."
	@echo "Frontend will be available at http://0.0.0.0:8099"
	@echo "Backend will be available at http://0.0.0.0:8098"
	@echo "Access from workstation using server IP, e.g. http://192.168.1.100:8099"
	@echo "Press Ctrl+C to stop both servers"
	cd frontend && npm run dev & \
	cd backend && go run . -config=../config/servers.yaml & \
	wait

# Run production build
run: build
	./labmgr -config=config/servers.yaml

# Install dependencies
install:
	cd backend && go mod tidy
	cd frontend && npm install

# Clean build artifacts
clean:
	rm -f labmgr labmgr-*
	@echo "(releases/ is kept: a host may be running one of those binaries)"
	rm -rf frontend/dist
	rm -rf frontend/node_modules
	rm -rf backend/static

# Development setup
setup: install
	@echo "Lab Manager setup complete!"
	@echo "Edit config/servers.yaml to configure your servers"
	@echo "Run 'make dev' to start in development mode"
	@echo "Run 'make build && ./labmgr' for production"

# Quick production build and run
prod: build run