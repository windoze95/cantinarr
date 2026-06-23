package remediation

import "github.com/windoze95/cantinarr-server/internal/ai"

// modelRate is a per-1M-token price pair (USD) for a model.
type modelRate struct {
	in  float64 // input $/1M tokens
	out float64 // output $/1M tokens
}

// modelRates maps a model id to its price, confirmed against the Claude API
// reference (claude-haiku-4-5 $1/$5, claude-sonnet-4-6 $3/$15, claude-opus-4-8
// $5/$25, claude-fable-5 $10/$50). The OpenAI/Gemini defaults are included so a
// non-Anthropic remediation provider still gets a best-effort cost; an unknown
// model returns ok=false and the Runner SKIPS the cost check rather than crash.
var modelRates = map[string]modelRate{
	// Anthropic
	"claude-haiku-4-5":  {in: 1, out: 5},
	"claude-sonnet-4-6": {in: 3, out: 15},
	"claude-opus-4-8":   {in: 5, out: 25},
	"claude-opus-4-7":   {in: 5, out: 25},
	"claude-opus-4-6":   {in: 5, out: 25},
	"claude-fable-5":    {in: 10, out: 50},
	// OpenAI / Gemini defaults (best-effort).
	"gpt-5.5":          {in: 5, out: 15},
	"gemini-3.5-flash": {in: 1, out: 5},
}

// Cache-token cost multipliers relative to the input rate (Anthropic billing):
// a cache write costs ~1.25x the base input rate (5-minute TTL) and a cache read
// ~0.1x. Providers that don't surface cache tokens report 0, so this is a no-op
// for them.
const (
	cacheWriteMultiplier = 1.25
	cacheReadMultiplier  = 0.10
)

// costMicros returns the accumulated cost of one model turn in millionths of a
// USD, and ok=false when the model's price is unknown (in which case the caller
// skips the cost check). The formula matches the design:
//
//	cost = in_rate*(input + cache_creation*1.25 + cache_read*0.1)/1e6
//	     + out_rate*output/1e6
func costMicros(model string, u ai.Usage) (micros int64, ok bool) {
	rate, found := modelRates[model]
	if !found {
		return 0, false
	}
	inputUnits := float64(u.InputTokens) +
		float64(u.CacheCreationTokens)*cacheWriteMultiplier +
		float64(u.CacheReadTokens)*cacheReadMultiplier
	usd := rate.in*inputUnits/1e6 + rate.out*float64(u.OutputTokens)/1e6
	return int64(usd * 1e6), true
}
