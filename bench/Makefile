.PHONY: isolated inprocess all clean stats

# Run isolated benchmark (3 containers: gRPC server, REST proxy, runner)
# Results written to bench/results/
# Resource limits: proxy 1 CPU/2GB, gRPC 2 CPUs/4GB, runner 3 CPUs/2GB
isolated:
	@mkdir -p results
	@# Start services in background
	docker compose up -d grpc-server rest-proxy
	@echo "Waiting for services..."
	@sleep 3
	@# Start resource monitoring in background
	@echo "timestamp,container,cpu_pct,mem_usage,mem_pct" > results/resources.csv
	@(while true; do \
		docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.MemPerc}}' \
			bench-rest-proxy-1 bench-grpc-server-1 2>/dev/null | \
			while IFS= read -r line; do \
				echo "$$(date +%s),$$line"; \
			done >> results/resources.csv; \
		sleep 1; \
	done) & echo $$! > results/.stats_pid
	@# Run benchmark
	docker compose up --build --abort-on-container-exit bench-runner || true
	@# Stop monitoring
	@kill $$(cat results/.stats_pid 2>/dev/null) 2>/dev/null || true
	@rm -f results/.stats_pid
	@# Generate resource summary
	@echo "" >> results/benchmark.txt
	@echo "=== Resource Usage ===" >> results/benchmark.txt
	@echo "Container limits: rest-proxy 1 CPU/2GB, grpc-server 2 CPUs/4GB" >> results/benchmark.txt
	@awk -F',' 'NR>1 && $$2 ~ /rest-proxy/ { \
		gsub(/%/,"",$$3); \
		cpu=$$3+0; \
		if(cpu>max_cpu) max_cpu=cpu; \
		sum_cpu+=cpu; n_cpu++; \
		split($$4,mem," / "); \
		gsub(/[A-Za-z]/,"",mem[1]); \
		m=mem[1]+0; \
		if(m>max_mem) max_mem=m; \
		sum_mem+=m; n_mem++; \
	} END { \
		if(n_cpu>0) { \
			printf "rest-proxy peak CPU:  %.1f%%\n", max_cpu; \
			printf "rest-proxy avg CPU:   %.1f%%\n", sum_cpu/n_cpu; \
			printf "rest-proxy peak RAM:  %.1f MiB\n", max_mem; \
			printf "rest-proxy avg RAM:   %.1f MiB\n", sum_mem/n_mem; \
		} \
	}' results/resources.csv >> results/benchmark.txt
	@awk -F',' 'NR>1 && $$2 ~ /grpc-server/ { \
		gsub(/%/,"",$$3); \
		cpu=$$3+0; \
		if(cpu>max_cpu) max_cpu=cpu; \
		split($$4,mem," / "); \
		gsub(/[A-Za-z]/,"",mem[1]); \
		m=mem[1]+0; \
		if(m>max_mem) max_mem=m; \
	} END { \
		if(max_cpu>0) { \
			printf "grpc-server peak CPU: %.1f%%\n", max_cpu; \
			printf "grpc-server peak RAM: %.1f MiB\n", max_mem; \
		} \
	}' results/resources.csv >> results/benchmark.txt
	@echo ""
	@echo "=== Results ==="
	@cat results/benchmark.txt
	@# Cleanup
	docker compose down

# Run in-process Go benchmarks (standard `go test -bench`)
# Single container: 2 CPUs, 4GB RAM
inprocess:
	docker compose --profile inprocess up --build bench-inprocess

# Run both
all: inprocess isolated

# Monitor resource usage live (run in separate terminal while benchmark runs)
stats:
	docker stats --format "table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}\t{{.NetIO}}" \
		bench-rest-proxy-1 bench-grpc-server-1 bench-bench-runner-1

clean:
	docker compose --profile inprocess down --rmi local
	rm -rf results/
