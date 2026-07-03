package session

import "strings"

var modelPricingByName = map[string]ModelPricing{
	"claude-fable-5":         {Model: "claude-fable-5", InputPerMTokUSD: 10.00, OutputPerMTokUSD: 50.00, CacheReadPerMTokUSD: 1.00, CacheWritePerMTokUSD: 12.50, ContextWindowTokens: 200_000},
	"claude-mythos-5":        {Model: "claude-mythos-5", InputPerMTokUSD: 10.00, OutputPerMTokUSD: 50.00, CacheReadPerMTokUSD: 1.00, CacheWritePerMTokUSD: 12.50, ContextWindowTokens: 200_000},
	"claude-opus-4-8":        {Model: "claude-opus-4-8", InputPerMTokUSD: 5.00, OutputPerMTokUSD: 25.00, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 6.25, ContextWindowTokens: 200_000},
	"claude-opus-4-7":        {Model: "claude-opus-4-7", InputPerMTokUSD: 5.00, OutputPerMTokUSD: 25.00, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 6.25, ContextWindowTokens: 200_000},
	"claude-opus-4-6":        {Model: "claude-opus-4-6", InputPerMTokUSD: 5.00, OutputPerMTokUSD: 25.00, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 6.25, ContextWindowTokens: 200_000},
	"claude-opus-4-5":        {Model: "claude-opus-4-5", InputPerMTokUSD: 5.00, OutputPerMTokUSD: 25.00, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 6.25, ContextWindowTokens: 200_000},
	"claude-sonnet-4-6":      {Model: "claude-sonnet-4-6", InputPerMTokUSD: 3.00, OutputPerMTokUSD: 15.00, CacheReadPerMTokUSD: 0.30, CacheWritePerMTokUSD: 3.75, ContextWindowTokens: 200_000},
	"claude-sonnet-4-5":      {Model: "claude-sonnet-4-5", InputPerMTokUSD: 3.00, OutputPerMTokUSD: 15.00, CacheReadPerMTokUSD: 0.30, CacheWritePerMTokUSD: 3.75, ContextWindowTokens: 200_000},
	"claude-haiku-4-5":       {Model: "claude-haiku-4-5", InputPerMTokUSD: 1.00, OutputPerMTokUSD: 5.00, CacheReadPerMTokUSD: 0.10, CacheWritePerMTokUSD: 1.25, ContextWindowTokens: 200_000},
	"gpt-5.5":                {Model: "gpt-5.5", InputPerMTokUSD: 5.00, OutputPerMTokUSD: 30.00, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 0, ContextWindowTokens: 1_000_000},
	"gpt-5.5-pro":            {Model: "gpt-5.5-pro", InputPerMTokUSD: 30.00, OutputPerMTokUSD: 180.00, CacheReadPerMTokUSD: 0, CacheWritePerMTokUSD: 0, ContextWindowTokens: 1_000_000},
	"gpt-5.4":                {Model: "gpt-5.4", InputPerMTokUSD: 2.50, OutputPerMTokUSD: 15.00, CacheReadPerMTokUSD: 0.25, CacheWritePerMTokUSD: 0, ContextWindowTokens: 128_000},
	"gpt-5.4-mini":           {Model: "gpt-5.4-mini", InputPerMTokUSD: 0.75, OutputPerMTokUSD: 4.50, CacheReadPerMTokUSD: 0.075, CacheWritePerMTokUSD: 0, ContextWindowTokens: 128_000},
	"gpt-5.4-nano":           {Model: "gpt-5.4-nano", InputPerMTokUSD: 0.20, OutputPerMTokUSD: 1.25, CacheReadPerMTokUSD: 0.02, CacheWritePerMTokUSD: 0, ContextWindowTokens: 128_000},
	"gpt-5.4-pro":            {Model: "gpt-5.4-pro", InputPerMTokUSD: 30.00, OutputPerMTokUSD: 180.00, CacheReadPerMTokUSD: 0, CacheWritePerMTokUSD: 0, ContextWindowTokens: 128_000},
	"gemini-3.1-pro-preview": {Model: "gemini-3.1-pro-preview", InputPerMTokUSD: 2.00, OutputPerMTokUSD: 12.00, CacheReadPerMTokUSD: 0.20, CacheWritePerMTokUSD: 0, ContextWindowTokens: 1_000_000},
	"gemini-3.1-flash-lite":  {Model: "gemini-3.1-flash-lite", InputPerMTokUSD: 0.25, OutputPerMTokUSD: 1.50, CacheReadPerMTokUSD: 0.025, CacheWritePerMTokUSD: 0, ContextWindowTokens: 1_000_000},
	"gemini-2.5-pro":         {Model: "gemini-2.5-pro", InputPerMTokUSD: 1.25, OutputPerMTokUSD: 10.00, CacheReadPerMTokUSD: 0.125, CacheWritePerMTokUSD: 0, ContextWindowTokens: 1_000_000},
	"gemini-2.5-flash":       {Model: "gemini-2.5-flash", InputPerMTokUSD: 0.30, OutputPerMTokUSD: 2.50, CacheReadPerMTokUSD: 0.03, CacheWritePerMTokUSD: 0, ContextWindowTokens: 1_000_000},
	"gemini-2.5-flash-lite":  {Model: "gemini-2.5-flash-lite", InputPerMTokUSD: 0.10, OutputPerMTokUSD: 0.40, CacheReadPerMTokUSD: 0.01, CacheWritePerMTokUSD: 0, ContextWindowTokens: 1_000_000},
	"grok-4-3":               {Model: "grok-4-3", InputPerMTokUSD: 1.25, OutputPerMTokUSD: 2.50, CacheReadPerMTokUSD: 0, CacheWritePerMTokUSD: 0, ContextWindowTokens: 256_000},
	"deepseek-chat":          {Model: "deepseek-chat", InputPerMTokUSD: 0.14, OutputPerMTokUSD: 0.28, CacheReadPerMTokUSD: 0.0028, CacheWritePerMTokUSD: 0, ContextWindowTokens: 64_000},
	"deepseek-reasoner":      {Model: "deepseek-reasoner", InputPerMTokUSD: 0.435, OutputPerMTokUSD: 0.87, CacheReadPerMTokUSD: 0.003625, CacheWritePerMTokUSD: 0, ContextWindowTokens: 64_000},
}

func lookupModelPricing(model string) (ModelPricing, bool) {
	price, ok := modelPricingByName[strings.TrimSpace(model)]
	return price, ok
}

func LookupModelPricing(model string) (ModelPricing, bool) {
	return lookupModelPricing(model)
}

func EstimateCostUSD(pricing ModelPricing, usage *TokenUsage) float64 {
	return estimateCostUSD(pricing, usage)
}

func estimateCostUSD(pricing ModelPricing, usage *TokenUsage) float64 {
	if usage == nil {
		return 0
	}
	const million = 1_000_000.0
	return (float64(usage.InputTokens) * pricing.InputPerMTokUSD / million) +
		(float64(usage.OutputTokens) * pricing.OutputPerMTokUSD / million) +
		(float64(usage.CacheReadTokens) * pricing.CacheReadPerMTokUSD / million) +
		(float64(usage.CacheWriteTokens) * pricing.CacheWritePerMTokUSD / million)
}
