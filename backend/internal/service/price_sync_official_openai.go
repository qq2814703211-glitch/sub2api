package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var officialOpenAIFields = []string{officialInput, officialOutput, officialCacheRead, officialCache30m, officialCache12h}
var officialOpenAIContextRE = regexp.MustCompile(`(?m)^Short context: ≤([0-9]+K?) input tokens\. Long context: >([0-9]+K?) input tokens\.$`)
var officialOpenAIWriteRE = regexp.MustCompile(`(?m)^- The cache write price is for 30 minutes of retention\. A 12-hour cache write costs (\$[0-9.]+) per million tokens for short-context requests at Standard pricing\.$`)
var officialOpenAIRuleRE = regexp.MustCompile(`(?m)^- For requests with more than ([0-9]+K?) input tokens, input and cache rates (double|increase to [0-9.]+ times the short-context rate) and output costs ([0-9.]+) times the short-context rate for the full request\.$`)

func officialOpenAIThreshold(raw string) (int, bool) {
	value, ok := strings.CutSuffix(raw, "K")
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 || n > 100_000_000 {
		return 0, false
	}
	if ok {
		n *= 1000
	}
	if n > 100_000_000 {
		return 0, false
	}
	return n, true
}

func loadOfficialOpenAI(ctx context.Context, f modelPricingSyncFetcher) (map[string]PriceSyncQuote, map[string]string, error) {
	pricing, err := officialFetch(ctx, f, officialOpenAIURL)
	if err != nil {
		return nil, nil, err
	}
	docs := make(map[string][]byte)
	failures := make(map[string]string)
	for _, model := range officialOpenAIModels {
		url := officialOpenAIModelURL(model)
		doc, err := officialFetch(ctx, f, url)
		if err != nil {
			failures[model] = fmt.Sprintf("%s: %v", url, err)
			continue
		}
		docs[model] = doc
	}
	quotes, problems := parseOfficialOpenAI(pricing, docs)
	for model, why := range failures {
		problems[model] = why
	}
	for model, q := range quotes {
		q.SourceURL = officialOpenAIURL + "#standard-pricing-data"
		q.Evidence = fmt.Sprintf("region=OpenAI API Standard; sha256:%x; model_rule=%s; sha256:%x", sha256.Sum256(pricing), officialOpenAIModelURL(model), sha256.Sum256(docs[model]))
		quotes[model] = q
	}
	return quotes, problems, nil
}

func parseOfficialOpenAI(pricing []byte, modelDocs map[string][]byte) (map[string]PriceSyncQuote, map[string]string) {
	quotes := make(map[string]PriceSyncQuote)
	problems := make(map[string]string)
	doc := string(pricing)
	contextRule := officialOpenAIContextRE.FindAllStringSubmatch(doc, -1)
	threshold, ok := 0, false
	if len(contextRule) == 1 && contextRule[0][1] == contextRule[0][2] {
		threshold, ok = officialOpenAIThreshold(contextRule[0][1])
	}
	prefix, _, found := strings.Cut(doc, "### Standard pricing data\n")
	if !ok || !found || !strings.Contains(prefix, "Prices per 1M tokens.") {
		for _, model := range officialOpenAIModels {
			problems[model] = "missing explicit USD Standard per-million pricing or input-token context boundary"
		}
		return quotes, problems
	}
	section := officialMarkdownSection(doc, "### Standard pricing data")
	want := []string{"Model", "Short context input", "Short context output", "Short context cached input", "Short context 30m write", "Short context 12h write", "Long context input", "Long context output", "Long context cached input", "Long context 30m write", "Long context 12h write"}
	labeled := false
	rows := map[string][][]string{}
	for _, line := range strings.Split(section, "\n") {
		row := officialMarkdownRow(line)
		if len(row) == 0 {
			labeled = false
			continue
		}
		if row[0] == "Model" {
			labeled = slices.Equal(row, want)
			continue
		}
		if slices.Contains(officialOpenAIModels, row[0]) {
			if !labeled {
				rows[row[0]] = append(rows[row[0]], nil)
				continue
			}
			rows[row[0]] = append(rows[row[0]], row)
		}
	}
	for _, model := range officialOpenAIModels {
		rr := rows[model]
		if len(rr) != 1 || len(rr[0]) != len(want) {
			problems[model] = "missing or ambiguous exact USD Standard short/long and 30m/12h row"
			continue
		}
		short, long := make(map[string]float64), make(map[string]float64)
		for i, key := range officialOpenAIFields {
			shortRate, shortOK := officialAmount(rr[0][i+1], "$", "")
			longRate, longOK := officialAmount(rr[0][i+1+len(officialOpenAIFields)], "$", "")
			if !shortOK || !longOK {
				problems[model] = "missing USD Standard short/long amount for " + key
				break
			}
			short[key], long[key] = shortRate/officialMillion, longRate/officialMillion
		}
		if len(short) != len(officialOpenAIFields) {
			continue
		}
		if err := checkOfficialOpenAIModel(string(modelDocs[model]), model, threshold, short, long); err != nil {
			problems[model] = err.Error()
			continue
		}
		q := officialQuote(model, "openai", "USD", "token", short)
		q.Tiers = []PriceSyncTier{{Threshold: threshold, Rates: long}}
		quotes[model] = q
	}
	return quotes, problems
}

func checkOfficialOpenAIModel(doc, model string, threshold int, short, long map[string]float64) error {
	if !slices.Contains(strings.Split(doc, "\n"), "Model ID: `"+model+"`") {
		return fmt.Errorf("exact OpenAI model-page identity missing")
	}
	pricing := officialMarkdownSection(doc, "## Pricing")
	write := officialOpenAIWriteRE.FindAllStringSubmatch(pricing, -1)
	rule := officialOpenAIRuleRE.FindAllStringSubmatch(pricing, -1)
	if len(write) != 1 || len(rule) != 1 {
		return fmt.Errorf("OpenAI model page lacks explicit 30m/12h or full-request long-context pricing rules")
	}
	boundary, ok := officialOpenAIThreshold(rule[0][1])
	if !ok || threshold != boundary {
		return fmt.Errorf("OpenAI model and price boundaries disagree")
	}
	twelve, ok := officialAmount(write[0][1], "$", "")
	if !ok || twelve/officialMillion != short[officialCache12h] {
		return fmt.Errorf("OpenAI model and price pages disagree on Standard 12h cache write")
	}
	metric := map[string]string{"Input": officialInput, "Output": officialOutput, "Cached input": officialCacheRead, "Cache writes": officialCache30m}
	seen := map[string]bool{}
	for _, line := range strings.Split(officialMarkdownSection(pricing, "### Text tokens"), "\n") {
		row := officialMarkdownRow(line)
		if len(row) == 0 {
			continue
		}
		key, ok := metric[row[0]]
		if !ok {
			continue
		}
		if seen[key] || len(row) != 3 || row[2] != "1M tokens" {
			return fmt.Errorf("ambiguous OpenAI model-page text token metric %s", row[0])
		}
		amount, ok := officialAmount(row[1], "$", "")
		if !ok || amount/officialMillion != short[key] {
			return fmt.Errorf("OpenAI model and price pages disagree on %s", key)
		}
		seen[key] = true
	}
	if len(seen) != len(metric) {
		return fmt.Errorf("short-context OpenAI model-page metric missing")
	}
	// Validate the explicit long-context table against the independently stated
	// model page rule. Never synthesize a missing cache-write column from a ratio.
	inputMultiplier := 2.0
	if rule[0][2] != "double" {
		var ok bool
		inputMultiplier, ok = officialAmount(rule[0][2], "increase to ", " times the short-context rate")
		if !ok {
			return fmt.Errorf("invalid OpenAI long-context input/cache multiplier")
		}
	}
	outputMultiplier, ok := officialAmount(rule[0][3], "", "")
	if !ok {
		return fmt.Errorf("invalid OpenAI long-context output multiplier")
	}
	for key, rate := range short {
		multiplier := inputMultiplier
		if key == officialOutput {
			multiplier = outputMultiplier
		}
		want := rate * multiplier
		if math.Abs(long[key]-want) > math.Max(math.Abs(want)*1e-10, 1e-20) {
			return fmt.Errorf("OpenAI model rule and explicit long-context price disagree on %s", key)
		}
	}
	return nil
}
