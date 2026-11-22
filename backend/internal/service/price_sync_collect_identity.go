package service

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

func priceSyncV2RegistryURLAllowed(raw string, native bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return false
	}
	if !native {
		return u.Host == "models.dev" && u.Path == "/api.json"
	}
	if u.Host != "raw.githubusercontent.com" {
		return false
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 7 || parts[1] != "basellm" || parts[2] != "llm-metadata" || strings.Join(parts[4:], "/") != "dist/api/all.json" {
		return false
	}
	if parts[3] == "main" {
		return true
	}
	return len(parts[3]) == 40 && strings.Trim(parts[3], "0123456789abcdef") == ""
}

// Providers encode endpoint identity, not the currency of a model's cost row.
// International endpoints of a domestic owner classify a SKU but cannot supply
// its China tariff. Untrusted providers (including plans and retailers) do neither.
var priceSyncV2ProviderOwners = map[string]string{
	"alibaba-cn": "alibaba", "alibaba": "alibaba",
	"bailing": "bailing", "deepseek": "deepseek", "longcat": "longcat",
	"minimax-cn": "minimax", "minimax": "minimax",
	"moonshotai-cn": "moonshotai", "moonshotai": "moonshotai",
	"sensenova": "sensenova", "stepfun": "stepfun", "stepfun-ai": "stepfun",
	"tencent-tokenhub": "tencent", "volcengine": "volcengine", "xiaomi": "xiaomi",
	"zhipuai": "zhipuai", "zai": "zhipuai",
	"openai": "openai", "anthropic": "anthropic", "google": "google",
	"xai": "xai", "mistral": "mistral", "cohere": "cohere", "meta": "meta",
	"azure": "microsoft", "amazon-bedrock": "amazon", "nova": "amazon",
}

var priceSyncV2NativePaygo = map[string]bool{
	"alibaba-cn": true, "bailing": true, "deepseek": true, "longcat": true,
	"minimax-cn": true, "moonshotai-cn": true, "sensenova": true, "stepfun": true,
	"tencent-tokenhub": true, "volcengine": true, "xiaomi": true, "zhipuai": true,
}

// These reserved model families cover gaps when a current source drops a row.
// Prefixes classify currency policy only; they never create or match a quote.
var priceSyncV2NativeFamilies = []struct{ prefix, owner string }{
	{"deepseek-", "deepseek"}, {"kimi-", "moonshotai"}, {"moonshot-", "moonshotai"},
	{"qwen", "alibaba"}, {"qvq-", "alibaba"}, {"qwq-", "alibaba"}, {"wan2", "alibaba"}, {"wanx", "alibaba"}, {"tongyi-", "alibaba"},
	{"glm-", "zhipuai"}, {"cogview-", "zhipuai"}, {"cogvideo", "zhipuai"},
	{"minimax-", "minimax"}, {"mimo-", "xiaomi"}, {"step-", "stepfun"},
	{"doubao-", "volcengine"}, {"seedance-", "volcengine"}, {"seedream-", "volcengine"},
	{"ling-", "bailing"}, {"ring-", "bailing"}, {"longcat-", "longcat"}, {"hunyuan-", "tencent"},
	{"stepaudio-", "stepfun"}, {"sensenova-", "sensenova"},
}

// Exact IDs that have no unambiguous brand prefix.
var priceSyncV2NativeIDs = map[string]string{
	"hy3": "tencent", "hy3-preview": "tencent", "hy4": "tencent", "hy4-preview": "tencent",
	"image-01": "minimax", "image-01-live": "minimax", "facechain-finetune": "alibaba", "happyoyster-1.0-adventure": "alibaba",
}

func priceSyncV2ProviderKnown(provider string) bool {
	return priceSyncV2ProviderOwners[provider] != ""
}

// This is a currency-policy classification. It supplies neither rates nor
// proof that a particular provider serves the model.
func priceSyncV2ReservedNativeOwner(model string) string {
	if owner := priceSyncV2NativeIDs[model]; owner != "" {
		return owner
	}
	for _, family := range priceSyncV2NativeFamilies {
		if strings.HasPrefix(model, family.prefix) {
			return family.owner
		}
	}
	return ""
}

func priceSyncV2Ownership(provider, model string) (owner string, native bool) {
	owner = priceSyncV2ProviderOwners[provider]
	// Domestic endpoints in these feeds do not carry first-party namespaced
	// SKUs. Aliyun, for example, lists siliconflow/deepseek-v3.2 as a resale.
	// Never strip that namespace to bind the row to its creator's SKU.
	if priceSyncV2NativePaygo[provider] && strings.Contains(model, "/") {
		return "", false
	}
	if expected := priceSyncV2ReservedNativeOwner(model); expected != "" {
		if owner != expected {
			return "", true
		}
		return owner, true
	}
	if provider == "azure" && !strings.HasPrefix(model, "phi-") && model != "model-router" {
		return "", false
	}
	if provider == "amazon-bedrock" {
		// Amazon's own model IDs, optionally scoped to an inference-profile region.
		id := model
		region, rest, found := strings.Cut(id, ".")
		if found && (region == "us" || region == "eu" || region == "jp" || region == "apac" || region == "global") {
			id = rest
		}
		if !strings.HasPrefix(id, "amazon.nova-") && !strings.HasPrefix(id, "amazon.titan-") {
			return "", false
		}
	}
	// Defend against a first-party provider listing another owner's hosted SKU.
	for _, family := range []struct{ prefix, owner string }{
		{"gpt-", "openai"}, {"claude-", "anthropic"}, {"gemini-", "google"},
		{"gemma-", "google"}, {"grok-", "xai"}, {"mistral-", "mistral"}, {"llama-", "meta"},
	} {
		if strings.HasPrefix(model, family.prefix) && owner != family.owner {
			return "", false
		}
	}
	return owner, priceSyncV2NativePaygo[provider] || owner == "alibaba" || owner == "moonshotai" || owner == "minimax" || owner == "stepfun" || owner == "zhipuai"
}

func priceSyncV2NeedsVendor(model, owner string) string {
	switch {
	case owner == "deepseek":
		return "DeepSeek China holiday/peak terms need the first-party tariff"
	case owner == "moonshotai" && (model == "kimi-k3.1-pro" || model == "kimi-k3.1-pro-highspeed"):
		return "Kimi China exact SKU requires the independently priced 5m and 1h writes"
	case owner == "openai" && (model == "gpt-6.2-sol" || model == "gpt-6.2-astra"):
		return "OpenAI exact SKU requires published 30m/12h and long-context components"
	case officialPriceRequiresVendor(model):
		return "exact SKU requires complete vendor tariff and units"
	}
	return ""
}

func priceSyncV2DeepSeekAmbiguousAlias(model string) bool {
	return model == "deepseek-v4.1-flash" || model == "deepseek-v4.1-flash-1017"
}

func (c *priceSyncV2Collector) collectArgonDefaultTransportAlias() {
	const base, alias = "gemini-4-argon", "gemini-4-argon-high"
	// The default Antigravity transport sends this exact ID for bare Argon.
	// The existing pricing policy explicitly uses the same rate for it.
	if domain.DefaultAntigravityModelMapping[base] != alias || domain.DefaultAntigravityModelMapping[alias] != alias ||
		normalizeGeminiThinkingTierAlias(alias) != base || c.claims[alias] != "" || c.result.Problems[alias] != "" {
		return
	}
	if _, exact := c.result.Quotes[alias]; exact {
		return
	}
	q, ok := c.result.Quotes[base]
	if !ok || q.Model != base || q.Provider != "google" || q.Currency != "USD" ||
		q.BillingMode != "token" || !priceSyncV2RegistryURLAllowed(q.SourceURL, false) {
		return
	}
	q.Model = alias
	q.Evidence += "; transport=antigravity; default_model_mapping=" + base + "->" + alias + "; existing_pricing_equivalence=" + base
	c.result.Quotes[alias] = q
}

// A backend version or account-specific route mapping is not evidence that a
// globally billed alias resolves to the same tariff on every route.
func (c *priceSyncV2Collector) reportUnverifiedAliases() {
	for _, model := range []string{"deepseek-v4.1-flash", "deepseek-v4.1-flash-1017"} {
		c.result.NativeModels[model] = true
		if model == "deepseek-v4.1-flash" {
			c.reject(model, "reference only; same requested name maps to distinct DeepSeek and OpenAI upstream SKUs")
		} else {
			c.reject(model, "reference only; China price table identifies a version, not this exact billable API ID")
		}
	}
	for _, family := range []struct {
		base   string
		levels []string
	}{
		{"gemini-4-argon", []string{"low", "medium", "high"}},
		{"gemini-4-carbon", []string{"low", "medium", "high"}},
		{"gemini-4-hydrogen", []string{"low", "medium", "high", "xhigh"}},
		{"gemini-4-neon", []string{"low", "medium", "high"}},
	} {
		for _, level := range family.levels {
			model := family.base + "-" + level
			if _, exact := c.result.Quotes[model]; !exact && c.result.Problems[model] == "" {
				c.result.Problems[model] = fmt.Sprintf("reference only: %s has no exact Google tariff or verified same-price transport/region mapping to %s", model, family.base)
			}
		}
	}
}
