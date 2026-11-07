# GPT Image 2.6 existing-protocol regression

## Scope and provenance

- Baseline: `8e92c892b47f3f934f18ce63da4c4dcb75f0b6bd`.
- Reviewed upstream PR: `https://github.com/Wei-Shaw/sub2api/pull/8529`.
- API verification on 2026-11-05: open, not merged; head `8daf5656622d73f81f22418f3efa439039f15af0`; base equals the baseline above.
- Fetch source: `git fetch https://github.com/Wei-Shaw/sub2api.git refs/pull/8529/head:refs/remotes/pr/8529`.
- The four production files and `openai_images_complexity_test.go` imported from that head match the PR byte for byte. The PR's unrelated README addition is excluded.
- Reused behavior: parse explicit `complexity`, preserve it in direct Codex Images and Responses image-generation tools, migrate top-level image-model Responses options, and retain 404/405 fallback behavior.

The added work covers `gpt-image-2.6`, `gpt-image-2.6-flare`, and `gpt-image-2.6-sunburst` in the backend model catalog and frontend whitelist. The bare name is a local custom alias, not an assertion that OpenAI publishes that SKU. Configure an account/channel mapping to a concrete model, or an explicit price entry when billing by the alias. No concrete-model or legacy token price is silently assigned to the bare name.

GPT Image 2.6 retains the existing OAuth/setup-token Responses bridge; this change does not infer support for the undocumented Codex direct Images endpoint. Existing 1.5/2/2.5 direct Images paths stay unchanged. The tests exercise existing protocol fields and behavior, not newly introduced SVG, animated GIF, or markup features.

## Primary sources and billing

Checked on 2026-11-05:

- `https://developers.openai.com/api/docs/models/gpt-image-2.6-flare.md`
- `https://developers.openai.com/api/docs/models/gpt-image-2.6-sunburst.md`
- `https://developers.openai.com/api/docs/guides/image-generation.md`
- Official OpenAPI schema at `openai/openai-openapi@3e288ba501d136b81181919a12a88495a0244488`, `openapi.yaml`: `ImageGenTool.model` enumerates both concrete 2.6 models.

The model pages distinguish image-only models from top-level Responses text drivers. The existing bridge uses a text driver and a separate image-generation tool. Mocked forwarding tests do not establish real account entitlement or upstream availability.

The integrated GloryCode candidate removes the implementation lane's USD fallback rates: this deployment settles one site dollar as one CNY. Configure the settlement price in the group/channel/catalog; exact operator prices retain precedence. The three new 2.6 names do not inherit an older image model's token rate. The historical per-image defaults are unchanged and are not claimed as official GPT Image 2.6 prices. The first image is charged normally.

## Executed regression coverage

All Go commands used the version declared in `backend/go.mod`: Go 1.27.3. Tests ran in `golang:1.27.3-alpine` with `--network none`, `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, `-mod=readonly`, `-tags unit`, and `-count=1`. Missing module dependencies were downloaded separately before the offline test run. No real image-generation request was sent.

New 2.6 coverage: **274 executed leaf cases**:

- 216 Images requests: 3 model names × API key/OAuth/setup-token × stream/nonstream × JSON/multipart × generation/edit × low/medium/high complexity. Assertions inspect the actual HTTP-upstream request, image/mask data, image count, usage, response body, and mapped model.
- 42 Responses requests: image-only model conversion, explicit text-driver image tool, and API-key passthrough, including stream/nonstream and mapped bare alias.
- 3 model identity/allowlist cases, 3 channel-to-account mapping cases, and 6 first-image usage-log and balance-deduction cases (token, configured per-image, historical default per-image).
- 2 official-price cases, 1 bare-alias pricing boundary, and 1 model-catalog case.

The imported PR contributes another 26 leaf cases, including omitted complexity, tool-option precedence, and 404/405 fallback. Existing regression coverage includes 2.5 direct Images, stream errors and partial output, usage accounting, media billing, and model routing. Chat Completions continues rejecting image-only models before scheduling.

### RED/GREEN evidence

- Unmodified baseline plus PR tests: 20 failed, 6 passed. Explicit `low`/`medium`/`high` was absent from outgoing JSON; the native-capability classification also failed. After porting: 26 passed.
- Concrete 2.6 pricing before the fix: expected text-input price `5e-6`, got stale legacy `2.5e-6`, both concrete models failed. After the fix: both passed, including no-remote-catalog billing and operator override precedence.
- Model catalog before the fix: the 2.6 membership test failed; frontend whitelist test also failed because `gpt-image-2.6` was absent. Both pass after catalog updates.
- Bare alias before the fix: expected no inferred rate, got the legacy `gpt-image-2` card. The explicit-only boundary now passes.

### Final results

- Combined focused Go regression: **535 top-level tests / 1,207 leaf cases passed**, no failures or skips, across service, handler, routes, OpenAI constants, and requestmodel packages.
- Full tests for the same five packages: **9,665 top-level tests passed; one top-level test failed; 12 skipped**. Leaf outcomes: **18,342 passed, 2 failed, 12 skipped**. The only remaining failure is `TestValidateCreateParams_CheckModeMatrix` (`probe_requires_api_key`, `quota_probe_requires_primary_model`): the offline container reaches endpoint DNS validation first and returns `CHANNEL_MONITOR_ENDPOINT_UNREACHABLE`. The same two assertions fail with baseline production sources restored using a Go overlay. The handler, routes, constants, and requestmodel full packages pass.
- An earlier full run hit the heap-threshold test `TestInflightEstimate_AccountMappingNoDBAndBoundedMemory` (`8666160` vs limit `8388608`). It passes in baseline isolation, candidate isolation, and the final full run. No unrelated memory-test behavior was changed.
- Frontend: 2 files / **62 tests passed** (`useModelWhitelist.spec.ts`, `ModelWhitelistSelector.spec.ts`); changed-file ESLint and `vue-tsc --noEmit` pass. Existing environment warnings concern Node localStorage and stale Browserslist data.
- `go vet -mod=readonly -tags unit` for all five packages, `gofmt`, and `git diff --check` pass.

No push or deployment was performed. Independent parent review is still appropriate before integration; the source audit here was performed by the implementing agent.

## Reproduction

From the repository root, prepare module caches separately if needed, then run offline:

```sh
docker run --rm --network none \
  -e GOTOOLCHAIN=local -e GOPROXY=off -e GOSUMDB=off \
  -v "$PWD/backend:/src" \
  -v xiao-release0033-gomod:/go/pkg/mod \
  -v xiao-release0033-gocache:/root/.cache/go-build \
  -w /src golang:1.27.3-alpine \
  go test -mod=readonly -tags unit \
  ./internal/service ./internal/pkg/openai ./internal/handler \
  ./internal/server/routes ./internal/pkg/requestmodel \
  -run '^(TestGPTImage|TestOpenAIImages|TestOpenAIGatewayServiceParseOpenAIImages|TestOpenAIGatewayServiceForwardImages|TestBuildOpenAIImages|TestParseOpenAIImages|TestForwardOpenAIImages|TestCodexDirectImages|TestCodexImages|TestImageStream|TestImagesOAuth|TestImageCount|TestOpenAIGatewayServiceRecordUsage|TestCalculateCost|TestComputeTokenBreakdown|TestGetModelPricing|TestOpenAIGatewayService_.*Image|TestChatCompletionsRejectsGPTImage|TestOpenAIChatCompletionsImage|TestGateway.*Image|TestGateway.*Video|Test.*Video.*Cost|Test.*Video.*Billing|TestDefaultModels)|^TestFromBody|^TestAllFromBody|^TestExtract|^TestGatewayRoutes|^TestComposite|^TestGatewayModels_Unmapped' \
  -count=1 -timeout=180s -json
```

Remove `-run ...` and use `-timeout=240s` for the recorded full-package run. Its two DNS-dependent assertions require an environment-aware baseline comparison as described above.

From `frontend`, using installed project dependencies:

```sh
pnpm exec vitest run src/composables/__tests__/useModelWhitelist.spec.ts \
  src/components/account/__tests__/ModelWhitelistSelector.spec.ts --reporter=verbose
pnpm exec eslint src/composables/useModelWhitelist.ts \
  src/composables/__tests__/useModelWhitelist.spec.ts
pnpm exec vue-tsc --noEmit
```
