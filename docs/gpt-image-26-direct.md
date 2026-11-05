# GPT-Image 2.6 direct Images compatibility

## Routing and fields

OAuth and setup-token requests to the Images generation/edit API use the Codex
`/backend-api/codex/images/generations` and `/backend-api/codex/images/edits`
endpoints for `gpt-image-2.6`, `gpt-image-2.6-flare`, and
`gpt-image-2.6-sunburst`. Routing uses the resolved account/channel model, so an
explicitly mapped bare alias follows its target. A 2.6 endpoint HTTP 404/405 does
not retry through the Responses image tool. Existing 2.5 routing and 404/405
fallback behavior remain covered by regression tests.

The direct request preserves quality (including `xhigh`/`max`), complexity,
`output_format`, integer `frame_count`/`frame_duration_ms`, and
`svg.model`/`svg.reasoning.effort`. Multipart SVG options accept dotted keys and
SDK-style `svg[model]`/`svg[reasoning][effort]` keys. Frame values reject fractions,
overflow, negative values and non-numbers; integral JSON decimal/exponent
notation is accepted without floating-point rounding. Format-specific ranges
and feature availability remain upstream decisions. No global SVG/GIF
combination restrictions are imposed on other compatible image providers.

Multipart `markup` is an uploaded asset. The direct JSON request represents it
as `markup: {image_url: <data URL>}`, separate from the source `images` array.
JSON callers may supply an object containing `image_url` or `file_id`. The
direct builder rejects scalar markup, ambiguous references, and markup combined
with a mask. It does not verify PNG transparency or matching dimensions locally.
The upstream still validates those asset constraints.

SVG and GIF base64-to-data-URL responses use `image/svg+xml` and `image/gif`,
including streaming completion events. Upstream HTTP 400 `invalid_value` and
`image_generation_user_error` remain HTTP 400.

## Protocol sources and limits

Checked on 2026-11-05:

- Official edit JSON reference: https://developers.openai.com/api/reference/resources/images/methods/edit.md documents markup as `{file_id, image_url}` and its mutual exclusion with mask.
- Official Python SDK: https://github.com/openai/openai-python/blob/main/src/openai/types/image_edit_params.py defines multipart markup as `FileTypes`, a transparent PNG asset.
- Official Codex client: https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/images.rs defines GIF frame controls as sibling fields alongside `output_format`; its typed edit client does not itself document markup.
- CLIProxyAPI fixed comparison: https://github.com/router-for-me/CLIProxyAPI/blob/010de194c294ad3b562b4c8de71f59740f5bc8c8/internal/runtime/executor/codex_openai_images.go establishes the 2.6 direct endpoint selection. Its multipart converter omits markup, so it was not used as evidence for markup wire encoding.

The official SDK/reference surfaces are not fully synchronized on SVG/GIF
options. Those values are preserved rather than constrained by a speculative
local allowlist. The API JSON reference establishes the markup object shape;
local HTTP tests verify gateway serialization, not live Codex acceptance.

This change does not translate top-level mixed Responses requests, establish
live upstream SVG/GIF entitlement, or add USD model prices. A mocked successful
GIF response proves formatting/forwarding only. Real upstream rejection (such
as the GIF HTTP 400 reported in CLIProxyAPI issue #7269) must remain visible.

## Verification

Baseline: `4452ab9acff4c0d843733387fdfa666174ebd20c`, branch
`fix/media-issue8534`; continued the existing six-file uncommitted implementation.

Execution environment: Docker `golang:1.27.3-alpine`, actual
`go version go1.27.3 linux/arm64`, memory 4 GiB, one CPU, `GOMAXPROCS=1`,
`GOGC=20`, `GOPROXY=off`, `GOSUMDB=off`, existing module/build cache volumes.
Commands below run from the backend directory in that container.

```sh
go test -mod=readonly -p 1 -tags unit -count=1 -run '^TestIssue8534' ./internal/service
# RED on inherited partial work: fractional/overflow frames, stringified markup,
# provider-wide format rejection and 2.6 Responses fallback assertions failed.

go test -mod=readonly -p 1 -tags unit -count=1 -json -timeout=180s \
  -run 'TestIssue8534|TestCodex.*Images|TestGPTImage26|TestGPT26|TestOpenAIImages|TestOpenAIImage' \
  ./internal/service
# Final exit 0: 582 leaf tests passed, 0 failed, 0 skipped.
# Includes all 216 Images protocol matrix cases, usage/settlement regressions,
# real local HTTP multipart PNG+markup transfer for OAuth/setup-token,
# SVG/GIF streamed MIME, both user-error HTTP 400 cases, and 2.5 fallback.

go test -mod=readonly -p 1 -run '^$' ./internal/service
# Final exit 0: untagged compilation, no tests selected by design.

git diff --check
# Exit 0.
```

Detailed raw evidence is retained in the parent task's
`xiao-media-build-20261105/issue8534` directory: `red-takeover.log`,
`green-takeover-final.jsonl`, and `untagged-compile-final.log`. No full-repository
suite, production push, or live paid provider generation was performed here.
