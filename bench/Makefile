.PHONY: isolated inprocess all clean stats

# Run isolated benchmark (3 containers: gRPC server, REST proxy, runner)
# Results written to bench/results/benchmark.txt
# Resource limits: proxy 1 CPU/2GB, gRPC 2 CPUs/4GB, runner 2 CPUs/2GB
isolated:
	@mkdir -p results
	docker compose up --build --abort-on-container-exit bench-runner
	@echo ""
	@echo "=== Results saved to bench/results/benchmark.txt ==="
	@cat results/benchmark.txt 2>/dev/null || true

# Run in-process Go benchmarks (standard `go test -bench`)
# Single container: 2 CPUs, 4GB RAM
inprocess:
	docker compose --profile inprocess up --build bench-inprocess

# Run both
all: inprocess isolated

# Monitor resource usage while benchmark is running (run in separate terminal)
stats:
	docker stats --format "table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}\t{{.NetIO}}" \
		bench-rest-proxy-1 bench-grpc-server-1 bench-bench-runner-1

clean:
	docker compose --profile inprocess down --rmi local
	rm -rf results/
