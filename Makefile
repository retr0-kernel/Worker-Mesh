.PHONY: proto-gen ent-gen build run clean test

# Generate protobuf code
proto-gen:
	protoc --go_out=. --go_opt=paths=source_relative proto/mesh.proto

# Generate ent code
ent-gen:
	go generate ./ent

# Generate all code
gen: proto-gen ent-gen

# Build the application
build: gen
	go build -o bin/worker cmd/worker/main.go

# Run the application
run: build
	./bin/worker

# Run multiple nodes for testing (different API ports)
run-node1: build
	./bin/worker -api-port 3001 -discovery-port 8081

run-node2: build
	./bin/worker -api-port 3002 -discovery-port 8082

run-node3: build
	./bin/worker -api-port 3003 -discovery-port 8083

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f proto/mesh.pb.go
	rm -f worker_*.db

# Run tests
test:
	go test -v ./...

# Show help
help:
	@echo "Available commands:"
	@echo "  make build      - Build the application"
	@echo "  make run        - Build and run a single node"
	@echo "  make run-node1  - Run node on port 3001"
	@echo "  make run-node2  - Run node on port 3002"
	@echo "  make run-node3  - Run node on port 3003"
	@echo "  make clean      - Clean build artifacts"
	@echo "  make test       - Run tests"
	@echo "  make gen        - Generate all code (protobuf + ent)"