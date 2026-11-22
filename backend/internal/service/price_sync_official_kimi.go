package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var officialDecimalRE = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

func officialAmount(raw, prefix, suffix string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, prefix) || !strings.HasSuffix(raw, suffix) {
		return 0, false
	}
	value := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, prefix), suffix))
	if !officialDecimalRE.MatchString(value) {
		return 0, false
	}
	n, err := strconv.ParseFloat(value, 64)
	return n, err == nil
}

func officialMarkdownRow(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return nil
	}
	fields := strings.Split(strings.Trim(line, "|"), "|")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	return fields
}

func officialMarkdownSection(doc, heading string) string {
	lines := strings.Split(doc, "\n")
	index := slices.IndexFunc(lines, func(line string) bool { return strings.TrimSpace(line) == heading })
	if index < 0 {
		return ""
	}
	level := strings.IndexByte(heading, ' ')
	if level < 1 {
		return ""
	}
	for i, line := range lines[index+1:] {
		n := strings.IndexByte(line, ' ')
		if n > 0 && n <= level && strings.Trim(line[:n], "#") == "" {
			return strings.Join(lines[index+1:index+1+i], "\n")
		}
	}
	return strings.Join(lines[index+1:], "\n")
}

func officialQuote(model, provider, currency, mode string, rates map[string]float64) PriceSyncQuote {
	return PriceSyncQuote{Model: model, Provider: provider, Currency: currency, BillingMode: mode, Rates: rates}
}

var officialKimiModels = []string{"kimi-k3.1-pro", "kimi-k3.1-pro-highspeed", "kimi-k3", "kimi-k2.7-code", "kimi-k2.7-code-highspeed", "kimi-k2.6"}
var officialKimiTables = regexp.MustCompile("_jsx\\(DocTable,\\{[^\n]*?columns:\\[(.*?)\\],rows:\\[(.*?)\\]\\}\\)\\}\\)")
var officialKimiColumns = regexp.MustCompile("\\{title:(?:`([^`]+)`|_jsx\\(Tooltip,\\{[^{}]+children:`([^`]+)`\\}\\)),width:`[^`]+`\\}")
var officialKimiRow = regexp.MustCompile("\\[(?:_jsx\\(_components\\.a,\\{href:`/guide/[^`]+`,children:`([a-z0-9.-]+)`\\}\\)|`([a-z0-9.-]+)`),((?:`[^`]*`,)*`[^`]*`)\\]")

// Mintlify serves its rendered table props in a JSON manifest within the
// official HTML. Decode the manifest, then read only literal DocTable data;
// never execute the compiled MDX or interpret it as general JavaScript.
func officialKimiSource(body []byte) (string, error) {
	z := html.NewTokenizer(bytes.NewReader(body))
	found := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				return "", z.Err()
			}
			return "", fmt.Errorf("Mintlify pricing manifest missing (Markdown/SSR shell has no prices)")
		case html.StartTagToken:
			token := z.Token()
			if token.Data != "template" {
				continue
			}
			for _, a := range token.Attr {
				if a.Key == "data-mint" && a.Val == "manifest" {
					found = true
					break
				}
			}
			if !found {
				continue
			}
			if z.Next() != html.TextToken {
				return "", fmt.Errorf("Mintlify manifest missing JSON text")
			}
			var manifest struct {
				Page struct {
					PagePath  string `json:"pagePath"`
					Locale    string `json:"locale"`
					Subdomain string `json:"subdomain"`
					MDX       struct {
						Source struct {
							Compiled string `json:"compiledSource"`
						} `json:"mdxSource"`
					} `json:"mdx"`
				} `json:"page"`
			}
			err := json.Unmarshal(z.Raw(), &manifest)
			if err != nil {
				return "", fmt.Errorf("invalid Mintlify manifest: %w", err)
			}
			p := manifest.Page
			if p.PagePath != "pricing/chat" || p.Locale != "zh" || p.Subdomain != "moonshotcn" || p.MDX.Source.Compiled == "" {
				return "", fmt.Errorf("unexpected Kimi region, path or manifest schema")
			}
			return p.MDX.Source.Compiled, nil
		}
	}
}

func parseOfficialKimi(body []byte) (map[string]PriceSyncQuote, map[string]string) {
	quotes := make(map[string]PriceSyncQuote)
	problems := make(map[string]string)
	doc, err := officialKimiSource(body)
	if err != nil {
		for _, model := range officialKimiModels {
			problems[model] = err.Error()
		}
		return quotes, problems
	}
	required := map[string]string{"模型": "", "输入": officialInput, "输出": officialOutput, "缓存命中": officialCacheRead, "缓存写入（5min）": officialCacheWrite, "缓存写入（1h）": officialCache1h}
	seen := make(map[string]bool)
	for _, table := range officialKimiTables.FindAllStringSubmatch(doc, -1) {
		columns := officialKimiColumns.FindAllStringSubmatch(table[1], -1)
		if len(columns) != 4 && len(columns) != 6 {
			continue
		}
		fields := make([]string, len(columns))
		labels := make(map[string]bool, len(columns))
		valid := true
		for i, col := range columns {
			label := col[1]
			if label == "" {
				label = col[2]
			}
			field, ok := required[label]
			if !ok || labels[label] || (i == 0) != (label == "模型") {
				valid = false
				break
			}
			labels[label] = true
			fields[i] = field
		}
		if !valid || !labels["输入"] || !labels["输出"] || !labels["缓存命中"] || (len(fields) == 6 && (!labels["缓存写入（5min）"] || !labels["缓存写入（1h）"])) {
			continue
		}
		for _, row := range officialKimiRow.FindAllStringSubmatch(table[2], -1) {
			model := row[1]
			if model == "" {
				model = row[2]
			}
			i := slices.Index(officialKimiModels, model)
			if i < 0 {
				continue
			}
			if seen[model] {
				delete(quotes, model)
				problems[model] = "duplicate official Kimi model rows"
				continue
			}
			seen[model] = true
			cells := strings.Split(row[3], "`,`")
			if len(cells) != len(fields)-1 || (i < 3 && len(fields) != 6) || (i >= 3 && len(fields) != 4) {
				problems[model] = "unexpected Kimi price/TTL columns"
				continue
			}
			rates := make(map[string]float64, len(cells))
			for c, cell := range cells {
				amount, ok := officialAmount(strings.Trim(cell, "`"), "¥", "/ MTok")
				if !ok {
					problems[model] = "missing or invalid CNY / MTok cell for " + fields[c+1]
					break
				}
				rates[fields[c+1]] = amount / officialMillion
			}
			if len(rates) == len(cells) {
				quotes[model] = officialQuote(model, "moonshotai", "CNY", "token", rates)
			}
		}
	}
	for _, model := range officialKimiModels {
		if !seen[model] {
			problems[model] = "exact model and complete labeled CNY / MTok columns missing from Kimi pricing DocTable"
		}
	}
	return quotes, problems
}
