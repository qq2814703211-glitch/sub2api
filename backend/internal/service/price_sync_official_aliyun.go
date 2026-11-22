package service

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
)

type officialAliyunSchema struct {
	heading, mode, field, unit, rule string
	headers                          []string
	models                           []string
}

var officialAliyunSchemas = []officialAliyunSchema{
	{"千问ASR", "per_second", "cost_per_second", "秒", "计费规则：按输入音频的秒数计费，输出不计费。", []string{"模型 ID（Model ID）", "输入单价", "输出单价"}, []string{"qwen3-asr-flash", "qwen3-asr-flash-filetrans"}},
	{"千问ASR-Realtime", "per_second", "cost_per_second", "秒", "计费规则：按输入音频的秒数计费，输出不计费。", []string{"模型 ID（Model ID）", "输入单价"}, []string{"qwen3-asr-flash-realtime"}},
	{"万相文生图", "per_image", "output_cost_per_image", "张", "仅输出计费，计费规则请参见[图像生成]", []string{"模型 ID（Model ID）", "输出单价"}, []string{"wan2.6-t2i", "wan2.5-t2i-preview", "wan2.2-t2i-plus", "wan2.2-t2i-flash", "wanx2.1-t2i-plus", "wanx2.1-t2i-turbo", "wanx2.0-t2i-turbo", "wanx-v1"}},
	{"人物写真生成-FaceChain", "per_request", "cost_per_request", "次", "facechain-finetune：按训练次数计费，请求失败不计费。", []string{"模型 ID（Model ID）", "单价"}, []string{"facechain-finetune"}},
}

var officialAliyunGaps = map[string]string{
	"qwen-image-3.0":            "Alibaba prices both input and output images; the single output-image mode cannot represent the complete quote",
	"qwen-image-3.0-pro":        "Alibaba prices both input and output images and different output resolutions; the single output-image mode cannot represent the complete quote",
	"happyoyster-1.0-adventure": "Alibaba lists mixed world-create per-request and experience per-second charges; one billing mode cannot represent the complete quote",
}

var officialAliyunModels = func() []string {
	var models []string
	for _, schema := range officialAliyunSchemas {
		models = append(models, schema.models...)
	}
	for model := range officialAliyunGaps {
		models = append(models, model)
	}
	slices.Sort(models)
	return models
}()

var officialAliyunHeading = regexp.MustCompile(`(?m)^#{1,3} ([^\r\n]+)$`)
var officialAliyunTab = regexp.MustCompile(`(?s)<Tab\s+title="([^"<>]+)">([^\x00]*?)</Tab>`)

func officialAliyunSections(doc string) map[string][]string {
	sections := make(map[string][]string)
	matches := officialAliyunHeading.FindAllStringSubmatchIndex(doc, -1)
	for i, m := range matches {
		title, _, _ := strings.Cut(doc[m[2]:m[3]], "<span")
		title = strings.TrimSpace(title)
		end := len(doc)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		sections[title] = append(sections[title], doc[m[1]:end])
	}
	return sections
}

type officialAliyunTableRow struct {
	cells           []string
	header, spanned bool
}

// The official Markdown document contains tables as HTML embedded in MDX.
// Tokenize only the Beijing tab. Never inherit a value from a previous row.
func officialAliyunTables(tab string) [][]officialAliyunTableRow {
	z := html.NewTokenizer(strings.NewReader(tab))
	var tables [][]officialAliyunTableRow
	var rows []officialAliyunTableRow
	row := officialAliyunTableRow{}
	var cell []byte
	inTable, inRow, inCell := false, false, false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return tables
		case html.StartTagToken:
			tag := z.Token()
			switch tag.Data {
			case "table":
				inTable = true
				rows = nil
			case "tr":
				if inTable {
					inRow = true
					row = officialAliyunTableRow{}
				}
			case "th", "td":
				if !inRow {
					continue
				}
				inCell = true
				cell = cell[:0]
				if tag.Data == "th" {
					row.header = true
				}
				for _, a := range tag.Attr {
					// The ASR-Realtime table attaches rowspan to a single
					// explicitly labeled row's input unit price. It is safe
					// to read that cell for this row, but not to fill other rows.
					if a.Key == "colspan" || (a.Key == "rowspan" && (row.header || len(row.cells) != 1)) {
						row.spanned = true
					}
				}
			}
		case html.TextToken:
			if inCell {
				cell = append(cell, z.Text()...)
				cell = append(cell, ' ')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "th", "td":
				if inCell {
					row.cells = append(row.cells, strings.Join(strings.Fields(string(cell)), " "))
					inCell = false
				}
			case "tr":
				if inRow {
					rows = append(rows, row)
					inRow = false
					inCell = false
				}
			case "table":
				if inTable {
					tables = append(tables, rows)
					inTable = false
					inRow = false
					inCell = false
				}
			}
		}
	}
}

func parseOfficialAliyun(body []byte) (map[string]PriceSyncQuote, map[string]string) {
	quotes := make(map[string]PriceSyncQuote)
	problems := make(map[string]string)
	for model, why := range officialAliyunGaps {
		problems[model] = why
	}
	sections := officialAliyunSections(string(body))
	for _, schema := range officialAliyunSchemas {
		rows, err := officialAliyunSectionRows(sections[schema.heading], schema)
		for _, model := range schema.models {
			if err != nil {
				problems[model] = err.Error()
				continue
			}
			var matches []officialAliyunTableRow
			for _, row := range rows {
				if len(row.cells) > 0 && row.cells[0] == model {
					matches = append(matches, row)
				}
			}
			if len(matches) != 1 {
				problems[model] = "missing or duplicate exact Beijing SKU row"
				continue
			}
			row := matches[0]
			if row.header || row.spanned || len(row.cells) != len(schema.headers)+1 {
				problems[model] = "Beijing SKU has ambiguous columns or a merged/multidimensional tariff"
				continue
			}
			if len(schema.headers) == 3 && row.cells[2] != "不计费" {
				problems[model] = "ASR has an additional or unknown output charge"
				continue
			}
			rate, ok := officialAmount(row.cells[1], "", "元/"+schema.unit)
			if !ok {
				problems[model] = "missing flat Beijing CNY amount per " + schema.unit
				continue
			}
			quotes[model] = officialQuote(model, "alibaba", "CNY", schema.mode, map[string]float64{schema.field: rate})
		}
	}
	return quotes, problems
}

func officialAliyunSectionRows(sections []string, schema officialAliyunSchema) ([]officialAliyunTableRow, error) {
	if len(sections) != 1 {
		return nil, fmt.Errorf("missing or ambiguous Alibaba %s pricing section", schema.heading)
	}
	section := sections[0]
	var beijing []string
	for _, tab := range officialAliyunTab.FindAllStringSubmatch(section, -1) {
		if tab[1] == "华北2（北京）" {
			beijing = append(beijing, tab[2])
		}
	}
	if len(beijing) != 1 {
		return nil, fmt.Errorf("missing or ambiguous Beijing pricing tab in %s", schema.heading)
	}
	beforeTabs, _, _ := strings.Cut(section, "<Tabs>")
	if !strings.Contains(beforeTabs, schema.rule) && !strings.Contains(beijing[0], schema.rule) {
		return nil, fmt.Errorf("expected one-meter billing rule missing from Beijing %s pricing", schema.heading)
	}
	tables := officialAliyunTables(beijing[0])
	if len(tables) != 1 || len(tables[0]) < 2 {
		return nil, fmt.Errorf("missing or ambiguous Beijing %s price table", schema.heading)
	}
	header := tables[0][0]
	want := len(schema.headers)
	if !header.header || header.spanned || len(header.cells) != want+1 || !slices.Equal(header.cells[:want], schema.headers) || !strings.HasPrefix(header.cells[want], "免费额度") {
		return nil, fmt.Errorf("unexpected Beijing %s table columns (refusing incomplete tariff)", schema.heading)
	}
	return tables[0][1:], nil
}
