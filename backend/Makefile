.PHONY: build generate test test-unit test-integration test-e2e test-db test-login-privacy

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

test-login-privacy:
	go test -tags=unit -count=1 -run '^(TestLoginDoesNotArchivePassword|TestLoginSessionDiscardLegacyEnvelopeAndConsumeOnce)$$' ./internal/handler ./internal/repository

test-integration:
	go test -tags=integration ./...

# Optional test profiles require their corresponding disposable stores/services.
test-db:
	@test -n "$${VIDEO_BILLING_TEST_DSN}" || { echo 'test-db requires VIDEO_BILLING_TEST_DSN'; exit 2; }
	@test -n "$${CACHE_BILLING_TEST_DSN}" || { echo 'test-db requires CACHE_BILLING_TEST_DSN'; exit 2; }
	@test -n "$${KEY_ROUTES_TEST_POSTGRES_DSN}" || { echo 'test-db requires KEY_ROUTES_TEST_POSTGRES_DSN'; exit 2; }
	@test -n "$${KEY_QUEUE_DATABASE_URL}" || { echo 'test-db requires KEY_QUEUE_DATABASE_URL'; exit 2; }
	@test -n "$${DIAGNOSTICS_TEST_DSN}" || { echo 'test-db requires DIAGNOSTICS_TEST_DSN'; exit 2; }
	@test -n "$${TICKET_TEST_DATABASE_URL}" || { echo 'test-db requires TICKET_TEST_DATABASE_URL'; exit 2; }
	@test -n "$${ACCOUNT_OPS_TEST_DSN}" || { echo 'test-db requires ACCOUNT_OPS_TEST_DSN'; exit 2; }
	@test -n "$${GROUP_RATE_HISTORY_TEST_POSTGRES_DSN}" || { echo 'test-db requires GROUP_RATE_HISTORY_TEST_POSTGRES_DSN'; exit 2; }
	@test -n "$${REQUEST_CAPTURE_TEST_DSN}" || { echo 'test-db requires REQUEST_CAPTURE_TEST_DSN'; exit 2; }
	@test -n "$${KEY_QUEUE_REDIS_ADDR}" || { echo 'test-db requires KEY_QUEUE_REDIS_ADDR'; exit 2; }
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
