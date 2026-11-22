# GPT-6.2 Sol

> For the complete documentation index, see [llms.txt](/llms.txt). Markdown versions of documentation pages are available by appending `.md` to the page URL.

> Our go-to model for everyday work, from coding to powering agents at scale.

Model ID: `gpt-6.2-sol`

GPT-6.2 Sol accepts text and image inputs and produces text. Use it with the
Responses API to call tools such as web search and computer use, or to delegate
work to subagents with the [Multi-agent beta](/api/docs/guides/responses-multi-agent).

The API rejects requests that set reasoning effort to `none`.

The Responses API supports [discounted background completion windows](/api/docs/guides/background#use-discounted-completion-windows) in public beta.

## Model details

- Input modalities: text, image
- Output modalities: text
- Unsupported modalities: audio, video
- 1,050,000 context window
- Maximum input tokens: 922,000
- 128,000 max output tokens
- Jun 01, 2026 knowledge cutoff
- Reasoning token support

## Pricing

Prices are per 1M tokens. See the [pricing page](/api/docs/pricing) for processing tiers and billing details.

### Text tokens

| Metric | Price | Unit |
| --- | ---: | --- |
| Input | $2 | 1M tokens |
| Cached input | $0.1 | 1M tokens |
| Cache writes | $2.5 | 1M tokens |
| Output | $10 | 1M tokens |

- The cache write price is for 30 minutes of retention. A 12-hour cache write costs $5 per million tokens for short-context requests at Standard pricing.
- For requests with more than 272K input tokens, input and cache rates double and output costs 1.5 times the short-context rate for the full request.
- Batch costs 50% of Standard rates. Fast mode costs twice the Standard rate for the same context length.
- Ultrafast costs 3 times the Standard rate through February 28, 2027, and 6 times the Standard rate starting March 1, 2027 (Pacific time).
