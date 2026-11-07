# GPT2.6 settlement regression and integration notes

Baseline: `f99a9c75783a9b2ae84ed24ea3f0822eed3db3d7`. Branch: `fix/media-gpt26-settlement`. Scope: preserve historical per-image prices and repair independent Responses hosted-image usage reaching billing. No production access, push, deployment, or upstream image requests.

## Implemented behavior

- `PricingService.GetImagePrice` returns only a scalar per-image price. Exact catalog entries win; the three configured 2.6 names retain the historical image-family per-image fallback (`gpt-image-2`, then 1.5, then 1). Token lookups still return nil when 2.6 has no explicit catalog entry. Group/card/channel priority, free overrides, shared/independent multipliers and 1K/2K/4K sizing remain intact.
- Responses `usage` belongs to the driver; `tool_usage.image_gen` is a separate aggregate across image calls. Add each component's total and modality/cache counters once. Do not multiply the tool aggregate by `ImageCount`, or add output-item representations again. The existing configured settlement card still prices the merged counters; this is not separate official driver-model and tool-model price selection.
- A bounded, immutable, request-local snapshot retains the driver and tool components. Repeated observations replace snapshots; absent components retain previous observations. Present tool zeroes replace prior tool counts. Existing base-model all-zero terminal fallback behavior is retained. No database field or migration is added; the snapshot is excluded from JSON.
- HTTP JSON, SSE, buffered SSE-to-JSON, API-key passthrough, ordinary WS and WS v2 relay are covered. WS v2 resets component counters each turn and suppresses retransmissions of the just-completed response ID before emitting another billing callback. This is a one-last-response guard, not unlimited-session historical-ID deduplication.
- Current hosted cache fields (`cached_tokens`, `cached_tokens_details.cached_image_tokens/cached_text_tokens`) take precedence over compatible nested image spellings. Explicit zero is respected. Omitted cache modality is not guessed. Totals and details are bounded; cached image input is removed from fresh image input exactly once by existing RecordUsage logic. No cache hit is inferred from image size or count.
- Canonical Responses totals/modalities win over Chat-compatible aliases even at zero. Existing wrapper precedence remains `usage`, `response.usage`, `data.usage`, `data.response.usage`.
- Dedicated Images OAuth/Responses bridge remains tool-only and keeps its existing atomic malformed/incomplete-tool fallback. It is deliberately not changed into mixed Responses billing.

## Primary evidence and boundaries

Read on 2026-11-05:

- Official generated schema: `https://raw.githubusercontent.com/openai/openai-python/main/src/openai/types/responses/response.py` (`ToolUsage`, `ToolUsageImageGen`). It explicitly defines tool usage as separate from base-model usage and image_gen as an aggregate. Optional output details and cache counts may be absent; absent cache means unknown. Captured under `official-response-schema.md` in the evidence directory.
- `https://raw.githubusercontent.com/openai/openai-python/main/src/openai/types/image_generate_params.py` defines `complexity`, `output_format=svg/gif`, nested `svg.model` / `svg.reasoning.effort`, `frame_count`, `frame_duration_ms`.
- `https://raw.githubusercontent.com/openai/openai-python/main/src/openai/types/image_edit_params.py` defines multipart `markup`; it cannot be combined with mask.
- The image-generation guide fetched in this lane still rendered a 2.5-focused body, while the parent independently retrieved the 2.6 guide. Do not claim this lane verified the guide's full 2.6 text. SDK sources above directly establish the tested parameter shapes.

API-key Images preserves JSON fields (including nested SVG settings) and multipart markup bytes on existing routing. The three added tests are passthrough contract pins, not real upstream feature/entitlement tests. No SVG/GIF/markup conversion was added to OAuth/Responses; no claim of complete 2.6 protocol support. Missing output modality details retain total tokens but do not infer an image-only breakdown. Upstream billing reconciliation for omitted cache detail or nonstandard aggregate semantics remains outside this regression.

## Real RED / GREEN

Evidence directory: `/Users/dev/Global-session/xiao-media-build-20261105/gpt26-settlement`.

- `red-independent.log`: original independent review probe, unmodified baseline. Legacy per-image expected 0.42, actual 0.134. API-key/OAuth × stream/nonstream: image input expected cost 10.56, actual 0.15; main text output cost 0; total 17.71.
- `green-independent-original.log`: the **same original probe**, injected by `green-independent-overlay.json`, passes on the candidate. Per-image amount 0.42; merged usage input 376/output 448; image input 352/output 439; text input cost 0.24, image input 10.56, text output 0.18, image output 17.56, total and single deduction 28.54. Prices 0.01/0.02/0.03/0.04 are explicit synthetic operator settlement prices, not published prices.
- `red-hosted.log`: wrapper/snapshot/cache regression fails on missing totals and cache fields. `red-merge-cache.log`: canonical zero loses to aliases; Images merge loses cache counters; tool-only SSE is dropped. `red-split-snapshots.log`: terminal driver-only snapshot loses prior tool usage, 376 becomes 5.
- `red-wsv2.log`: repeated terminal incorrectly emits three turns instead of two; first tool usage is lost. WS relay test and real WS adapter→RecordUsage test pass after the fix.
- `focused-final.jsonl`: 519 top-level / 1,213 leaf cases passed before the final Images fallback compatibility fix. `green-wsv2-full.jsonl`: complete WS v2 package 60 top-level / 67 leaf cases passed. `race-final.jsonl`: 86 top-level / 515 leaf cases passed, no race reports.
- `full-service.jsonl`: 8,867 top-level passes, 19 skips, 3 failing top-level tests. One was a real interaction with the dedicated Images atomic fallback; fixed and retested in `green-atomic-images.jsonl` (15 top-level / 180 leaf passes). The other failures are the known offline endpoint-DNS assertions and a global heap threshold (8,647,736 against 8,388,608). `baseline-environment.jsonl` and `candidate-environment.jsonl` both fail only the same two DNS assertions; the heap test passes isolated on both. This lane does not claim a completely green full-service suite.
- Final concurrent rebuild attempts were killed by the compiler (`signal: killed`), recorded in `final-regression.jsonl` / `final-race.jsonl`. They are build-resource failures, not behavioral RED evidence. The final serial run (`final-serial.jsonl`) passes: 521 top-level / 1222 leaf cases, no failures or skips. Parent integration should run the final combined suite.

All test runs use Go 1.27.3, `--network none`, `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, `-mod=readonly -tags unit -count=1`. Race uses the pre-existing `xiao-poison-evidence-race:go1.27.3` image. Tests use real forwarding/parser/billing code with local test upstreams and billing repository stubs; this lane did not run PostgreSQL persistence tests.

## RelayKit and resource rules

`git grep -n -i relaykit -- backend go.work go.mod package.json docs/agent-resource-safety.md` finds only the two rule paragraphs in `docs/agent-resource-safety.md`; no usable dependency or repository entry point exists in the searched backend. The RelayKit-first rule is therefore **not implemented as an engineering dependency here**. This change follows the user's explicit authorization to repair two billing defects and the full existing Responses chain using targeted existing parsers. It is new upstream usage compatibility behavior, not a storage-only exception; no approved waiver is claimed. The parent must explicitly resolve this rule deviation.

Request/response conversion and emitted payloads remain unchanged. No new goroutine, queue, timer, network operation, cache or retained response body is introduced. State is constant-sized per request/turn; parsing remains proportional to a bounded existing frame. Race tests exercise snapshot copying and existing relay lifecycles. No new production transport limit or lifecycle setting is introduced.

## Integration intersections

- `billing_service.go`: only `getDefaultImagePrice` switches to `GetImagePrice`; retain engine-lane changes elsewhere.
- `openai_gateway_service.go`: only two lines inside `OpenAIUsage` (private snapshot field/comment); retain runtime-lane `MediaUsage` and other result fields.
- `openai_images.go`: only `mergeOpenAIUsage` reuses the cache-complete merge helper.
- `openai_images_responses.go`: only dedicated Images atomic fallback preservation; retain unrelated runtime wiring.
- `openai_gateway_passthrough.go`: terminal usage assignment calls the component-aware replacement.
- WS v2 adapter adds `ImageCacheReadTokens` in its two existing projection literals. WS relay changes are usage extraction, component reset and duplicate-current-response guard, not transport redesign.

Do not overwrite whole shared files. Apply this commit with ordinary contextual merge and rerun the parent's integrated regression suite.
