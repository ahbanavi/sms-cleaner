package classify

import (
	_ "embed"
	"encoding/json"
)

// seed_rules.json holds keyword rules mined from real Iranian SMS (labeled
// automatically, then kept only when they hit >= 90% one category on the
// labeled set).
//
//go:embed seed_rules.json
var seedRules []byte

// SeedRules returns the built-in starter rules.
func SeedRules() []Rule {
	var raw []Rule
	_ = json.Unmarshal(seedRules, &raw)
	for i := range raw {
		raw[i].Source = "seed"
		raw[i].Enabled = true
	}
	return raw
}
