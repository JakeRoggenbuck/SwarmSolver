GRAPH ?= instances/dsjc125.5.col
SERVER ?= http://localhost:8080

.PHONY: build test server swarm swarm-llm load solo tail demo clean

build:
	go build -o bin/ ./cmd/...

test:
	go test -race ./...

server: build
	./bin/swarm-server -graph $(GRAPH)

# Heuristic-only swarm (no API key needed).
swarm: build
	./bin/swarm-agent -server $(SERVER) -heuristic 16

# Claude-planned swarm: Haiku workers plus Sonnet/Opus strategists.
swarm-llm: build
	./bin/swarm-agent -server $(SERVER) -haiku 16 -sonnet 3 -opus 1 -heuristic 4

# Fan-out benchmark. Use a scratch server: NOTE traffic lands in the log.
load: build
	ulimit -n 10240; ./bin/swarm-load -server $(SERVER) -clients 500 -publishers 20 -rate 50 -duration 20s

# Baseline: Z3 alone on the full graph.
solo: build
	./bin/swarm-solo -graph $(GRAPH) -k 13 -timeout 60s

tail: build
	./bin/swarm-tail -server $(SERVER)

demo:
	scripts/demo.sh

clean:
	rm -rf bin
