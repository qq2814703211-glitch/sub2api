.PHONY: build generate test test-unit test-integration test-e2e test-db

VERSION ?= $(shell ./scripts/resolve-version.sh)
LDFLAGS ?= -s -w -X main.Version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o bin/server ./cmd/server

generate:
	go generate ./ent
	go generate ./cmd/server

test:
	go test ./...
	golangci-lint run ./...

test-unit:
	go test -tags=unit ./...

test-integration:
	go test -tags=integration ./...

# Optional test profiles require their corresponding disposable stores/services.
test-db:
	go test -tags='unit,videodb' ./internal/repository -run '^TestVideoDB' -count=1
	go test -tags='unit,cachebillingdb' ./internal/repository -run '^TestGeminiCachedContentDB_' -count=1
	go test -tags=unit ./internal/repository -run '^TestAPIKeyRoutesPostgres$$|^TestAPIKeyConcurrencyDatabaseMigrationAndRepository$$|^TestConnectionDiagnosticsPostgres'
	go test -tags=unit ./internal/repository -run '^TestTicketPostgres'
	go test -tags=unit ./internal/repository ./internal/accountopstest -run '^TestAccountOpsProducerDatabaseDeliveryRecovery$$'
	go test -tags=unit ./internal/repository -run '^TestAPIKeyQueueRealRedis$$'
	go test -tags=dbtest ./internal/repository/group_rate_history_db_test -run '^Test(GroupRateHistory|ListGroupRateChangesSince|GroupSignals)'
	go test -tags=integration ./internal/requestcapture -run '^TestRequestCaptureSQL'

test-e2e:
	./scripts/e2e-test.sh

test-e2e-local:
	go test -tags=e2e -v -timeout=300s ./internal/integration/...
