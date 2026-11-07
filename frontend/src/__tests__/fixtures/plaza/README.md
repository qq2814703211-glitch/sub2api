# Synthetic cross-layer media fixtures

These are serialized outputs from the production Go service/DTO and admin HTTP handlers, using synthetic prices. They are not supplier prices or a live configuration.

- `plaza-token.json` and `ui-plaza-actual-dto.json`: `TestUIReviewMediaScheduleResponse` then `TestPlazaMediaServiceDTO`; effective period factor 3 and actual cost 3.
- `dto-endpoint-*.json`: `TestPlazaEndpointRateCosts` then `TestPlazaEndpointProductionDTO`; prices, prompt fee and reference-input surcharge share exactly one endpoint factor and one time factor.
- `ui-plaza-rates-dto.json`: `TestPlazaMediaRateOptions`; request/token mixed rules disclose endpoint alternatives and retain explicit zero.
- `ui-scheduled-*-http-response.json`: `TestUIReviewScheduledGroupHTTP` / `TestUIReviewScheduledChannelHTTP`; real PostgreSQL POST/GET response with media input-fee fields and a persisted schedule.

Default Vitest runs use the captured responses. Set `PLAZA_FIXTURE_DIR` to an external output directory to consume freshly generated responses. Run service tests and DTO tests in order with `UI_REVIEW_OUTPUT` set to that directory; then run the scheduled admin HTTP tests with `MEDIA_PRICING_TEST_POSTGRES_DSN` pointing to a disposable database. The real form tests write `ui-save-*.json` into `PLAZA_FIXTURE_DIR`. Re-run the HTTP tests to submit those payloads, verify raw database values, and export `ui-reloaded-*.json`; a final form pass opens those actual GET responses.

The channel HTTP fixture substitutes only its disposable group association ID when replaying a UI payload. Pricing fields are replayed unchanged. Account-stat cards deliberately have no time schedule.
