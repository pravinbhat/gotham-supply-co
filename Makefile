.PHONY: up down status smoke build test check-determinism clean

up:            ## build + start all services (reads .env / TEMPORAL_* env)
	@scripts/dev.sh up
down:          ## stop all services
	@scripts/dev.sh down
status:
	@scripts/dev.sh status
smoke:         ## place an order and follow it end-to-end
	@scripts/smoke.sh

build:
	cd services/batcave-order-ops && go build -o bin/order-ops ./cmd/server
	cd services/wayne-billing && mvn -q -DskipTests package
	cd services/batcave-armory && uv sync

check-determinism: ## static check for non-deterministic Go workflow code
	cd services/batcave-order-ops && go run go.temporal.io/sdk/contrib/tools/workflowcheck@latest ./...

clean:         ## wipe local SQLite data + logs
	rm -rf data .logs .run
