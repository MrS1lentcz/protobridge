.PHONY: isolated inprocess clean

# Run isolated benchmark (3 containers: gRPC server, REST proxy, runner)
# REST proxy: 1 CPU, 2GB RAM
# gRPC server: 2 CPUs, 4GB RAM
isolated:
	docker compose up --build --abort-on-container-exit bench-runner

# Run in-process Go benchmarks (standard `go test -bench`)
# Single container: 2 CPUs, 4GB RAM
inprocess:
	docker compose --profile inprocess up --build bench-inprocess

# Run both
all: inprocess isolated

clean:
	docker compose --profile inprocess down --rmi local
