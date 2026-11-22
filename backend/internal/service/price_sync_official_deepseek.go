package service

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var officialDeepSeekModels = []string{"deepseek-flash", "deepseek-v4-pro", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp", "deepseek-v4.1-flash", "deepseek-v4.1-flash-1017"}
var officialDeepSeekModelCell = regexp.MustCompile(`^([a-z0-9.-]+)(?:\([0-9]+\))?$`)
var officialDeepSeekVersion = regexp.MustCompile(`^DeepSeek-[A-Za-z0-9.-]+$`)
var officialDeepSeekAliases = regexp.MustCompile("(?m)^\\([0-9]+\\) 模型名请使用 `([^`]+)`。旧模型名 (.+?) 仍可调用，但对应模型已下线，请求将由 (DeepSeek-[A-Za-z0-9.-]+) 模型提供服务，并按 Flash 价格计费。$")
var officialDeepSeekAliasID = regexp.MustCompile("`([a-z0-9.-]+)`")
var officialDeepSeekSchedule = regexp.MustCompile(`(?m)^\([0-9]+\) 空闲时段价格为高峰时段价格的一半。北京时间周一至周五（不含中国法定节假日）([0-9: ,，、-]+) 为高峰时段；其余时段，包括周末及中国法定节假日全天均为空闲时段。$`)
var officialDeepSeekRange = regexp.MustCompile(`^(\d{1,2}):(\d{2})\s*-\s*(\d{1,2}):(\d{2})$`)

func officialDeepSeekWindows(doc string) ([]PriceSyncWindow, error) {
	rules := officialDeepSeekSchedule.FindAllStringSubmatch(doc, -1)
	if len(rules) != 1 {
		return nil, fmt.Errorf("missing or ambiguous Beijing weekday/Chinese-holiday peak-window rule")
	}
	var windows []PriceSyncWindow
	lastEnd := -1
	parts := strings.FieldsFunc(rules[0][1], func(c rune) bool { return c == '、' || c == '，' || c == ',' })
	if len(parts) == 0 || len(parts) > 24 {
		return nil, fmt.Errorf("invalid number of DeepSeek peak windows")
	}
	for _, part := range parts {
		r := officialDeepSeekRange.FindStringSubmatch(strings.TrimSpace(part))
		if len(r) == 0 {
			return nil, fmt.Errorf("invalid DeepSeek peak window")
		}
		h1, _ := strconv.Atoi(r[1])
		m1, _ := strconv.Atoi(r[2])
		h2, _ := strconv.Atoi(r[3])
		m2, _ := strconv.Atoi(r[4])
		start, end := 60*h1+m1, 60*h2+m2
		if h1 >= 24 || h2 >= 24 || m1 >= 60 || m2 >= 60 || start >= end || start < lastEnd {
			return nil, fmt.Errorf("invalid or overlapping DeepSeek peak windows")
		}
		lastEnd = end
		windows = append(windows, PriceSyncWindow{Timezone: "Asia/Shanghai", Weekdays: []int{1, 2, 3, 4, 5}, Start: fmt.Sprintf("%02d:%02d", h1, m1), End: fmt.Sprintf("%02d:%02d", h2, m2), ExcludeCNHolidays: true})
	}
	return windows, nil
}

func parseOfficialDeepSeek(body []byte) (map[string]PriceSyncQuote, map[string]string) {
	quotes := make(map[string]PriceSyncQuote)
	problems := map[string]string{
		"deepseek-v4.1-flash":      "DeepSeek documents the deepseek-flash API name; this shortened version is not a documented API ID",
		"deepseek-v4.1-flash-1017": "DeepSeek documents DeepSeek-V4.1-Flash-1017 as a backend version, not as a callable API ID; exact site route mapping is unverified",
	}
	doc := officialMarkdownSection(string(body), "## 模型细节")
	windows, windowErr := officialDeepSeekWindows(doc)
	type column struct {
		index      int
		version    string
		base, peak map[string]float64
		issue      string
	}
	cols := map[string]*column{}
	foundHeader := false
	for _, line := range strings.Split(doc, "\n") {
		row := officialMarkdownRow(line)
		if len(row) < 4 {
			continue
		}
		label := strings.Join(strings.Fields(row[0]), "")
		switch {
		case label == "模型" && row[1] == "模型" && row[2] == "模型":
			if foundHeader {
				for _, c := range cols {
					c.issue = "duplicate DeepSeek model header"
				}
				continue
			}
			foundHeader = true
			for i, cell := range row[3:] {
				id := officialDeepSeekModelCell.FindStringSubmatch(cell)
				if len(id) == 0 || (id[1] != "deepseek-flash" && id[1] != "deepseek-v4-pro") {
					continue
				}
				if c := cols[id[1]]; c != nil {
					c.issue = "duplicate DeepSeek model column"
					continue
				}
				cols[id[1]] = &column{index: i + 3, base: map[string]float64{}, peak: map[string]float64{}}
			}
		case label == "模型版本":
			for _, c := range cols {
				if c.index >= len(row) || !officialDeepSeekVersion.MatchString(row[c.index]) || c.version != "" {
					c.issue = "missing or ambiguous DeepSeek model version"
					continue
				}
				c.version = row[c.index]
			}
		case strings.HasPrefix(label, "价格"):
			field := ""
			switch strings.Join(strings.Fields(row[1]), "") {
			case "百万tokens输入/（缓存命中）":
				field = officialCacheRead
			case "百万tokens输入/（缓存未命中）":
				field = officialInput
			case "百万tokens输出":
				field = officialOutput
			}
			if field == "" {
				continue
			}
			for _, c := range cols {
				rates := c.base
				switch row[2] {
				case "空闲时段":
				case "高峰时段":
					rates = c.peak
				default:
					c.issue = "unknown DeepSeek price window"
					continue
				}
				amount, ok := 0.0, false
				if c.index < len(row) {
					amount, ok = officialAmount(row[c.index], "", "元")
				}
				if _, exists := rates[field]; exists || !ok {
					c.issue = "duplicate or invalid CNY per-million DeepSeek price for " + field
					continue
				}
				rates[field] = amount / officialMillion
			}
		}
	}
	for _, model := range officialDeepSeekModels[:2] {
		c := cols[model]
		switch {
		case windowErr != nil:
			problems[model] = windowErr.Error()
		case c == nil:
			problems[model] = "official DeepSeek model column missing"
		case c.issue != "":
			problems[model] = c.issue
		case c.version == "" || len(c.base) != 3 || len(c.peak) != 3:
			problems[model] = "DeepSeek version or full off-peak/peak CNY rates missing"
		case !officialDeepSeekPeakMatches(c.base, c.peak):
			problems[model] = "DeepSeek table and stated off-peak half-price rule disagree"
		default:
			q := officialQuote(model, "deepseek", "CNY", "token", c.base)
			for _, w := range windows {
				w.Rates = maps.Clone(c.peak)
				q.Windows = append(q.Windows, w)
			}
			quotes[model] = q
		}
	}
	notes := officialDeepSeekAliases.FindAllStringSubmatch(doc, -1)
	aliases := map[string]bool{}
	if flash, ok := quotes["deepseek-flash"]; ok && len(notes) == 1 && notes[0][1] == flash.Model && notes[0][3] == cols[flash.Model].version {
		for _, id := range officialDeepSeekAliasID.FindAllStringSubmatch(notes[0][2], -1) {
			aliases[id[1]] = true
		}
		for _, model := range officialDeepSeekModels[2:4] {
			if aliases[model] {
				q := cloneOfficialQuote(flash)
				q.Model = model
				quotes[model] = q
			}
		}
	}
	for _, model := range officialDeepSeekModels[2:4] {
		if _, ok := quotes[model]; !ok {
			problems[model] = "DeepSeek exact callable alias and current Flash version/price relationship not confirmed"
		}
	}
	return quotes, problems
}

func officialDeepSeekPeakMatches(base, peak map[string]float64) bool {
	for field, v := range base {
		if math.Abs(v*2-peak[field]) > v*1e-12 {
			return false
		}
	}
	return true
}
