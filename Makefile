.PHONY: help up down status smoke build check-determinism clean
.DEFAULT_GOAL := help

WORKFLOWCHECK_VERSION := v0.6.0

help:              ## list available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

up:                ## build + start all services (reads .env / TEMPORAL_* env)
	@scripts/dev.sh up
down:              ## stop all services
	@scripts/dev.sh down
status:            ## health-check each service
	@scripts/dev.sh status
smoke:             ## place an order and follow it end-to-end
	@scripts/smoke.sh

build:             ## build all services without starting them
	cd services/batcave-order-ops && go build -o bin/order-ops ./cmd/server
	cd services/wayne-billing && mvn -q -DskipTests package
	cd services/batcave-armory && uv sync

check-determinism: ## static check for non-deterministic Go workflow code
	cd services/batcave-order-ops && go run go.temporal.io/sdk/contrib/tools/workflowcheck@$(WORKFLOWCHECK_VERSION) ./...

clean:             ## stop services, then wipe local SQLite data + logs
	@scripts/dev.sh down
	rm -rf data .logs .run
