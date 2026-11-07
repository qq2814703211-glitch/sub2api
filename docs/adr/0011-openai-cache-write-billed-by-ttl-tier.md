# OpenAI 缓存写入按 30 分钟 / 12 小时分档计价，与 Anthropic 分字段

OpenAI 的缓存写入不是单一倍率：默认 30 分钟写入是输入价的 1.25×，12 小时写入是 2.5×（Responses API 通过 `prompt_cache_options.ttl = "12h"` 请求），而缓存读在两种 TTL 下同价（0.1×，GPT-6.1 Sol/Terra 为 0.05×）。写入价是总价，不是在输入价上加收；30 分钟升 12 小时会重新计一次 12 小时写入，不抵扣。

我们已有的 `CacheCreationPricePerToken` / `CacheCreation5mPrice` / `CacheCreation1hPrice` 是 **Anthropic** 语义（5 分钟 / 1 小时 ephemeral 缓存），两者不是同一种东西，因此不复用同一组字段。

决定：新增 `CacheWrite30mTokens` / `CacheWrite12hTokens` 两个独立字段（DTO、`usage_logs` 列、展示）与对应的两个单价，按 OpenAI 口径计价；`prompt_cache_options` 不再被当作不支持字段无条件删除，OpenAI 平台透传，其它平台仍删。上游不返回写入分档时只按缓存读计费，不臆造写入量。

代价：账本与展示多两列，历史数据在这两列为 0（旧请求本就没有这个口径），报表需要能区分"当时没有这个口径"和"真的一分没写"。
