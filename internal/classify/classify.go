// Package classify decides what an SMS is: spam, offer, archive (service
// notices), keep (human-written) or unknown. User rules come first and are
// explainable; a naive Bayes model trained on labeled messages fills the gaps.
package classify

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/ahbanavi/sms-cleaner/internal/textnorm"
)

// Categories.
const (
	Spam    = "spam"
	Offer   = "offer"
	Archive = "archive"
	Keep    = "keep"
	Unknown = "unknown"
)

// Labels are the categories a user (or the model) can assign.
var Labels = []string{Spam, Offer, Archive, Keep}

// IsLabel reports whether s is one of Labels.
func IsLabel(s string) bool {
	for _, l := range Labels {
		if s == l {
			return true
		}
	}
	return false
}

// Rule kinds.
const (
	KindKeyword     = "keyword"      // substring of the normalized body
	KindRegex       = "regex"        // RE2 regex on the normalized body
	KindSender      = "sender"       // exact sender: hard override
	KindSenderRegex = "sender_regex" // RE2 regex on the canonical sender
)

// Rule is one user/seed rule.
type Rule struct {
	ID       int64   `json:"id"`
	Kind     string  `json:"kind"`
	Pattern  string  `json:"pattern"`
	Category string  `json:"category"`
	Weight   float64 `json:"weight"`
	Source   string  `json:"source"` // user | seed | learned
	Enabled  bool    `json:"enabled"`
	Note     string  `json:"note,omitempty"`
	Hits     int     `json:"hits"`
}

// Validate checks a rule before it is stored.
func (r *Rule) Validate() error {
	if !IsLabel(r.Category) {
		return fmt.Errorf("unknown category %q", r.Category)
	}
	r.Pattern = strings.TrimSpace(r.Pattern)
	if r.Pattern == "" {
		return fmt.Errorf("empty pattern")
	}
	switch r.Kind {
	case KindKeyword:
		if len([]rune(textnorm.Text(r.Pattern))) < 2 {
			return fmt.Errorf("keyword too short")
		}
	case KindSender:
	case KindRegex, KindSenderRegex:
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return fmt.Errorf("bad regex: %w", err)
		}
	default:
		return fmt.Errorf("unknown rule kind %q", r.Kind)
	}
	if r.Weight == 0 {
		r.Weight = 2
	}
	return nil
}

type compiled struct {
	Rule
	norm, noSpace string
	re            *regexp.Regexp
}

// Config tunes decisions.
type Config struct {
	MinScore         float64 // top score needed to decide (default 2)
	MinMargin        float64 // lead over the runner-up (default 1)
	BayesWeight      float64 // Bayes probability is multiplied by this (default 3)
	BayesMinExamples int     // Bayes ignored below this many examples (default 20)
	SpamMinScore     float64 // spam blocks+deletes, so it needs more evidence (default 3)
}

func (c *Config) defaults() {
	if c.MinScore == 0 {
		c.MinScore = 2
	}
	if c.MinMargin == 0 {
		c.MinMargin = 1
	}
	if c.BayesWeight == 0 {
		c.BayesWeight = 3
	}
	if c.BayesMinExamples == 0 {
		c.BayesMinExamples = 20
	}
	if c.SpamMinScore == 0 {
		c.SpamMinScore = 3
	}
}

// Classifier is immutable once built; rebuild it when rules or labels change.
type Classifier struct {
	cfg     Config
	senders map[string]compiled
	rules   []compiled
	bayes   *Bayes
}

// New compiles rules and trains Bayes.
func New(cfg Config, rules []Rule, examples []Example) *Classifier {
	cfg.defaults()
	c := &Classifier{cfg: cfg, senders: map[string]compiled{}, bayes: TrainBayes(examples)}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		cr := compiled{Rule: r}
		switch r.Kind {
		case KindSender:
			c.senders[textnorm.Sender(r.Pattern)] = cr
			continue
		case KindKeyword:
			cr.norm = textnorm.Text(r.Pattern)
			cr.noSpace = textnorm.NoSpace(cr.norm)
		case KindRegex, KindSenderRegex:
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				continue
			}
			cr.re = re
		}
		c.rules = append(c.rules, cr)
	}
	return c
}

// Bayes exposes the trained model (for term hints and stats).
func (c *Classifier) Bayes() *Bayes { return c.bayes }

// Reason explains one contribution to a decision.
type Reason struct {
	Source   string  `json:"source"` // sender | rule | bayes
	RuleID   int64   `json:"rule_id,omitempty"`
	Detail   string  `json:"detail"`
	Category string  `json:"category"`
	Weight   float64 `json:"weight"`
}

// Result is a classification with its explanation.
type Result struct {
	Category   string             `json:"category"`
	Confidence float64            `json:"confidence"`
	Scores     map[string]float64 `json:"scores"`
	Reasons    []Reason           `json:"reasons"`
}

// Summary is a short human-readable reason string.
func (r Result) Summary() string {
	var parts []string
	for _, rs := range r.Reasons {
		if rs.Category == r.Category {
			parts = append(parts, rs.Detail)
		}
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, "; ")
}

// Classify scores one SMS.
func (c *Classifier) Classify(sender, body string) Result {
	s := textnorm.Sender(sender)
	if r, ok := c.senders[s]; ok {
		return Result{
			Category: r.Category, Confidence: 1,
			Scores:  map[string]float64{r.Category: 100},
			Reasons: []Reason{{Source: "sender", RuleID: r.ID, Detail: "sender " + r.Pattern + " is always " + r.Category, Category: r.Category, Weight: 100}},
		}
	}
	norm := textnorm.Text(body)
	noSpace := textnorm.NoSpace(norm)
	scores := map[string]float64{}
	var reasons []Reason
	for _, r := range c.rules {
		hit := false
		switch r.Kind {
		case KindKeyword:
			hit = strings.Contains(norm, r.norm) || strings.Contains(noSpace, r.noSpace)
		case KindRegex:
			hit = r.re.MatchString(norm)
		case KindSenderRegex:
			hit = r.re.MatchString(s)
		}
		if hit {
			scores[r.Category] += r.Weight
			reasons = append(reasons, Reason{Source: "rule", RuleID: r.ID, Detail: fmt.Sprintf("%s “%s”", kindLabel(r.Kind), r.Pattern), Category: r.Category, Weight: r.Weight})
		}
	}
	if c.bayes.Examples() >= c.cfg.BayesMinExamples {
		// A two-word SMS carries little evidence; damp the model on short texts.
		scale := math.Min(1, float64(len(textnorm.Tokens(norm)))/5)
		for cat, p := range c.bayes.Predict(sender, body) {
			w := math.Round(p*c.cfg.BayesWeight*scale*100) / 100
			if w <= 0.05 {
				continue
			}
			scores[cat] += w
			if p >= 0.5 {
				reasons = append(reasons, Reason{Source: "bayes", Detail: fmt.Sprintf("learned model %.0f%%", p*100), Category: cat, Weight: w})
			}
		}
	}
	sort.SliceStable(reasons, func(i, j int) bool { return reasons[i].Weight > reasons[j].Weight })

	top, second := "", ""
	for _, cat := range Labels {
		if top == "" || scores[cat] > scores[top] {
			top, second = cat, top
		} else if second == "" || scores[cat] > scores[second] {
			second = cat
		}
	}
	res := Result{Category: Unknown, Scores: scores, Reasons: reasons}
	total := 0.0
	for _, v := range scores {
		total += v
	}
	if total > 0 {
		res.Confidence = math.Round(scores[top]/total*100) / 100
	}
	need := c.cfg.MinScore
	if top == Spam {
		need = math.Max(need, c.cfg.SpamMinScore)
	}
	if scores[top] >= need && scores[top]-scores[second] >= c.cfg.MinMargin {
		res.Category = top
	}
	return res
}

func kindLabel(k string) string {
	switch k {
	case KindRegex:
		return "pattern"
	case KindSenderRegex:
		return "sender pattern"
	default:
		return k
	}
}

// Suggest returns candidate keyword chips for a message: its words and
// two-word phrases, each annotated with which category the model thinks it
// points to.
func (c *Classifier) Suggest(body string) []TermLean {
	toks := textnorm.Tokens(textnorm.Text(body))
	seen := map[string]bool{}
	var out []TermLean
	add := func(t string) {
		if seen[t] || strings.Contains(t, "#") || strings.Contains(t, "%") {
			return
		}
		if parts := strings.Fields(t); len(parts) == 1 && stopwords[t] || len(parts) == 2 && stopwords[parts[0]] && stopwords[parts[1]] {
			return
		}
		seen[t] = true
		out = append(out, c.bayes.Lean(t))
	}
	for i, t := range toks {
		add(t)
		if i+1 < len(toks) {
			add(t + " " + toks[i+1])
		}
	}
	return out
}

// MatchingRules lists rule IDs that fire for a message (for "why" views and hit counts).
func (r Result) MatchingRules() []int64 {
	var ids []int64
	for _, rs := range r.Reasons {
		if rs.RuleID != 0 {
			ids = append(ids, rs.RuleID)
		}
	}
	return ids
}

// stopwords are too common to be useful as keywords on their own.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`از به با در برای که و را این آن تا روی همه هم یا ما شما من او بر یک های ها می است شد شده شود بود
		کرد کنید کنیم دارد های رو هر اگر یا نیز پس سپس فقط خود ای ان اون همین چه چون the a an to of and or in on for is are be your you
		with at by from this that it as our we`) {
		stopwords[w] = true
	}
}
