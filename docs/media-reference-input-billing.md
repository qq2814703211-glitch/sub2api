# Reference-input media billing

Media pricing uses the site's configured settlement unit. This extension adds JSON fields to existing `media_pricing` storage; no database migration or supplier defaults are installed.

## Configuration

| JSON field | Go field | Behavior |
| --- | --- | --- |
| `extra_input_image_price` | `ExtraInputImagePrice *float64` | Non-negative finite price per input image above the allowance. Missing/null disables this surcharge; explicit zero is retained. |
| `free_input_images` | `FreeInputImages *int` | Non-negative input-image allowance per successful request; missing/null means zero. |
| `duration_source` | `BillingDuration MediaBillingDuration` | `output` (default) or `total`. Total is valid only with `second` rules. |

For at least one successful output, the input-image surcharge is:

`max(input_image_count - free_input_images, 0) * extra_input_image_price`

It contributes to `CostBreakdown.ImageInputCost` once per request, including when outputs select different pixel/layer rules. It is not a per-output charge or `PromptPrice`. The first successful output remains charged normally; requests with no successful output incur no surcharge. The existing selected multipliers apply to the combined cost.

Total-second rules use `total_duration_seconds` once for the request. A reported total already includes reference video and is not multiplied by output count. Without that total, exact output durations are summed and input reference-video duration is added once. Mixed output tariffs cannot allocate a request total and return `ErrMediaUsageIncomplete`. Duration conditions still refer to output duration, not the total.

## Usage and trust

- `input_image_count` (`*int`), `input_duration_seconds` and `total_duration_seconds` (`*float64`) preserve unknown versus explicit zero across clone, merge and pending JSON.
- Existing `duration_seconds` remains pure output duration.
- Requests count actual image fields/arrays, multipart image files and content image blocks, including first/last frames and references. Native content takes precedence over compatible top-level aliases. Masks do not count. Opaque drafts leave counts unknown.
- Client-provided `usage`, counter fields and totals are never used as settlement evidence. Reference-video duration comes from actual video entries with explicit duration/seconds; bare video URLs leave duration unknown. A complete request with no video proves zero input seconds.
- Trusted upstream response `usage.input_images`, `usage.input_image_count` and native `task.usage` override request evidence, including zero. Native task usage takes precedence. JSON and completed-image SSE paths preserve these fields.
- MiniMax-H3 terminal settlement does not substitute the requested duration for measured output seconds. Missing required image counts or total/input/output measurements returns a diagnostic error. Async pending remains available and the claim is released for retry.
- Failed/cancelled/nonterminal tasks and image failures never trigger the surcharge. A later complete success settles once under the stable task request ID, with durable ledger deduplication after cache claim loss.

## UI and persistence

Channel, group and account-statistics cards reuse the full media object. Clones copy the new pointers; DTOs and JSONB round trips retain explicit zero. The JSON editor validates prices, integer allowances and the duration enum. Chinese/English displays show request-level input fees separately from output rules and disclose total seconds. Existing raw form factories, submit mapping and reload paths are exercised rather than bypassed.

## Verification

Use the repository's pinned pnpm and Go toolchain. Relevant Go packages: `internal/service`, `internal/handler`, `internal/repository`, `internal/handler/admin`, `internal/server/routes` with `-tags unit`.

- `TestMediaReference*`: input trust/counting, unknown/zero semantics, clone/cache, allowance boundaries, total vs output, multipliers, submit/pending, handler-to-ledger and repeated polls.
- `TestMediaRuntimeAPIKeyImagesToActualCost`: JSON/multipart/SSE forwarding through actual RecordUsage; partial successes, first-output fees, independent image multiplier and account-statistics fees.
- `TestMediaReferenceHandlerPostgresLedgerIdempotency`: real PostgreSQL billing repository; balance and dedup row read back after repeated polls and claim loss.
- `TestMediaPricing*Postgres*`: admin HTTP validation/create/update/omitted-update/clear plus raw channel, group and account-statistics JSONB and native readers.
- Optional review fixtures: `MEDIA_PRICING_REVIEW_FIXTURE` exercises every card/rule; `MEDIA_PRICING_REVIEW_V2_FIXTURE` also verifies literal input-fee boundary amounts and MiniMax totals. Fixtures are review-only, not defaults or evidence of live supplier calls.
- Frontend tests cover valid/invalid JSON, zero/null, actual create/edit/submit/reload flows and translated displayed prices. Run `pnpm run typecheck` as well.

PostgreSQL tests require `MEDIA_PRICING_TEST_POSTGRES_DSN` pointing to a disposable test database. They explicitly skip without it. Upstream HTTP is fixture-based; no paid/live generation is part of this suite.
