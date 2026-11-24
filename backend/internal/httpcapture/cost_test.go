package httpcapture

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/tobilg/ai-observer/internal/anthropicwire"
	"github.com/tobilg/ai-observer/internal/api"
	"github.com/tobilg/ai-observer/internal/pricing"
)

func usageDetail(raw string) *api.HTTPUsageDetail {
	u := anthropicwire.ParseUsage(json.RawMessage(raw))
	d := &api.HTTPUsageDetail{}
	d.Usage, d.Totals, d.Quality = u, u.Totals(), api.UsageFinal
	return d
}

func component(d *api.HTTPUsageDetail, name string) *api.HTTPCostComponent {
	for i := range d.CostComponents {
		if d.CostComponents[i].Component == name {
			return &d.CostComponents[i]
		}
	}
	return nil
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-12+1e-9*math.Abs(b) }

// 1-hour cache writes cost twice the input price of the model and context
// tier the catalog gives for the prompt size (the catalog has no 1-hour
// price). The first usage is a request of a Claude Code subscription login,
// where every cache write was 1h.
func TestPriceUsageCacheWrite1h(t *testing.T) {
	const model = "claude-haiku-5-5"
	cases := []struct {
		name                                  string
		input, output, read, write5m, write1h int64
	}{
		{"claude code subscription", 2, 25, 32048, 0, 365},
		{"large prompt (long context tier when the model has one)", 1000, 10, 0, 0, 150000},
		{"5m and 1h", 10, 1, 0, 100, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{
				"input_tokens": c.input, "output_tokens": c.output, "cache_read_input_tokens": c.read,
				"cache_creation_input_tokens": c.write5m + c.write1h,
				"cache_creation":              map[string]int64{"ephemeral_5m_input_tokens": c.write5m, "ephemeral_1h_input_tokens": c.write1h},
			})
			d := usageDetail(string(raw))
			priceUsage(d, model)
			p := pricing.ClaudePricingForPrompt(model, c.input+c.read+c.write5m+c.write1h)
			if p == nil {
				t.Fatalf("%s not in the catalog", model)
			}
			want := float64(c.input)*p.InputCostPerToken + float64(c.output)*p.OutputCostPerToken +
				float64(c.read)*p.CacheReadCostPerToken + float64(c.write5m)*p.CacheWriteCostPerToken +
				float64(c.write1h)*2*p.InputCostPerToken
			if d.CostStatus != api.CostComplete || d.CostReason != "" {
				t.Fatalf("status %s: %s", d.CostStatus, d.CostReason)
			}
			if d.CostUSD == nil || !near(*d.CostUSD, want) {
				t.Fatalf("cost %v, want %v", d.CostUSD, want)
			}
			w := component(d, "cache_write_1h")
			if w == nil || w.PricePerMTok == nil || !near(*w.PricePerMTok, 2*p.InputCostPerToken*1e6) || w.Note == "" {
				t.Fatalf("1h component %+v", w)
			}
		})
	}
	// without 1h writes there is no 1h component
	d := usageDetail(`{"input_tokens":10,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":0},"output_tokens":1}`)
	priceUsage(d, model)
	if component(d, "cache_write_1h") != nil || d.CostStatus != api.CostComplete {
		t.Fatalf("%+v", d)
	}
}
