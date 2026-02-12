.PHONY: build run dev clean install build-static

# Build everything with embedded assets
build: build-frontend build-backend

# Build frontend
build-frontend:
	cd frontend && npm install && npm run build

# Build backend with embedded assets
build-backend: build-frontend
	rm -rf backend/static
	cp -r frontend/dist backend/static
	cd backend && go mod tidy
	cd backend && CGO_ENABLED=0 go build -ldflags="-w -s" -o ../labmgr .

# Build static binary (same as build, but explicit)
build-static: build-frontend
	rm -rf backend/static
	cp -r frontend/dist backend/static
	cd backend && go mod tidy
	cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o ../labmgr-linux-amd64 .

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