.PHONY: dev wait seed down test

API_URL ?= http://localhost:8080
DATABASE_URL ?= postgres://sagalore:sagalore@127.0.0.1:5432/sagalore?sslmode=disable

dev: 
	docker compose up --build -d
	$(MAKE) wait seed
	@echo ""
	@echo "API is up at $(API_URL)"
	@echo "Start the companion against Classic Era:"
	@echo "  SAGALORE_DEV_TOKEN=dev-token SAGALORE_ADDON_SRC=addon/Sagalore go run ./cmd/sagalore --api-url $(API_URL) --client classic_era"

wait:
	@i=0; until curl -sf $(API_URL)/healthz >/dev/null; do \
		i=$$((i+1)); \
		if [ $$i -gt 60 ]; then echo "API did not answer $(API_URL)/healthz"; exit 1; fi; \
		sleep 2; \
	done

seed:
	SAGALORE_BOOTSTRAP_GROUP=tutorial:Tutorial DATABASE_URL="$(DATABASE_URL)" go run ./api seed

down:
	docker compose down

test:
	go test ./...
	lua5.1 addon/objectives_test.lua
	lua5.1 addon/chains_test.lua
	lua5.1 addon/invite_test.lua
