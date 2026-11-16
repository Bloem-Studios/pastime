.PHONY: typecheck web build generate check-generated i18n i18n-check test test-integration vet bench licenses dev-env dev-up dev-test-db dev-down

# The web build is embedded into the binary; .keep keeps the embed directory present.
typecheck:
	pnpm exec tsc --noEmit -p web/tsconfig.json

web: typecheck
	pnpm exec vite build --config web/vite.config.ts
	touch internal/webassets/dist/.keep

build: web
	go build -trimpath -o bin/pastime ./cmd/pastime

generate:
	python3 tools/api-contract.py
	go generate ./...
	pnpm api:types

check-generated: i18n-check
	python3 tools/check-generated.py

# i18n refreshes the catalogs and lists missing translations; i18n-check fails on gaps.
i18n:
	node web/tools/i18n-extract.mjs

i18n-check:
	node web/tools/i18n-extract.mjs --check

test: web
	pnpm test:unit
	python3 tests/deploy/test_install.py
	go test -race -timeout 20m ./...

test-integration: web
	pnpm test:unit
	python3 tests/deploy/test_install.py
	@test -n "$$PASTIME_TEST_DATABASE_URL" || (echo 'Export the isolated test database URL first'; exit 1)
	@test -n "$$PASTIME_TEST_VALKEY_URL" || (echo 'Export the isolated Valkey URL first'; exit 1)
	PASTIME_REQUIRE_INTEGRATION=1 go test -race -count=1 -p 2 -timeout 20m ./...

vet:
	go vet ./...

bench:
	go test ./internal/httpapi -run '^$$' -bench . -benchmem -count=3

licenses:
	python3 tools/licenses.py

dev-env:
	python3 tools/dev-env.py

dev-up:
	docker compose --env-file .env -f compose.dev.yaml up -d --wait

dev-test-db:
	docker compose --env-file .env -f compose.dev.yaml exec -T postgres createdb -U pastime pastime_test

dev-down:
	docker compose --env-file .env -f compose.dev.yaml down
