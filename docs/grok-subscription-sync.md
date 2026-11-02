# Grok OAuth live subscription synchronization

## Problem and scope

Grok OAuth accounts can retain a paid `credentials.subscription_tier` after the subscription ends. A fresh access token may omit the `tier` claim, and refreshing billing windows does not by itself supply the current subscription. The account badge and usage panel can consequently keep showing Heavy.

This patch is based on production commit `172e847650e266e16844b79ba44cc8ee0b0ace47` (`0.2.19-GloryCode-0.0.38`). It is prepared on the independent branch `fix/grok-subscription-sync` for later integration. It does not change the application version or require a database migration.

## Upstream findings

- [Sub2API PR #5571](https://github.com/Wei-Shaw/sub2api/pull/5571) added access-token tier parsing and refresh-time replacement. Its relevant changes are already included in the base commit. Keeping the old tier when the token omits the claim leaves the reported downgrade case unresolved.
- [Sub2API PR #8343](https://github.com/Wei-Shaw/sub2api/pull/8343) concerns billing-window scheduling thresholds; it was open when investigated and is separate from subscription identity.
- The official [Grok Build subscription checker](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-shell/src/agent/subscription_check.rs) reads `/user?include=subscription` independently of the JWT.
- The official [Grok Build billing extension](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-shell/src/extensions/billing.rs) enriches billing with `RemoteSettings.subscription_tier_display`. Its [tier classifier](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-shell/src/tier.rs) treats the tier as a free-form display string.

## Data contract

`extra.grok_subscription_snapshot` records successful explicit settings observations. It keeps the canonical tier, original display name, source, request-start time, success-observation time, HTTP status and account identity scope. Subscription freshness is independent of weekly and monthly billing freshness.

A successful `/settings` response with a nonempty subscription display is evidence of the current tier. A missing JWT claim, zero monthly allowance, HTTP error, malformed response or missing settings field is not evidence of Free. The last successful observation keeps its own timestamp when a later refresh fails.

A matching live snapshot takes precedence over legacy JWT and credential hints. Persistence updates subscription fields narrowly and preserves tokens, model mappings and unrelated metadata. Credential/proxy changes and out-of-order observations are checked at the persistence boundary; scheduler publication follows the committed change.

## Product names

An account name such as `Grok-Ultra-13` is an operator label. It does not determine the subscription. Confirmed Free results display Free; confirmed Heavy results remain Heavy. Explicit Ultra and future provider display names are retained without inventing a new numeric JWT tier or rewriting Heavy as Ultra.

## Integration and verification

The feature is intended to update subscription alongside billing refresh, refresh it during manual account checks, and schedule bounded refreshes from ordinary request observations. Frontend badges and usage panels consume the same subscription observation, and local refresh logic must avoid recursively forcing another billing probe when timestamps change.

Regression coverage includes the no-tier JWT plus stale Heavy failure, Heavy controls, explicit Free, Ultra and future names, independent billing/settings failures, stale identity and observation ordering, narrow persistence and outbox atomicity, manual/ordinary request wiring, and frontend cache/list synchronization.

The task handoff records the exact integrated commit and executed test outcomes. Production account values remain unchanged until this work is integrated and released separately.
