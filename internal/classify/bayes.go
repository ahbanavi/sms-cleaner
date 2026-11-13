package classify

import (
	"math"
	"sort"

	"github.com/ahbanavi/sms-cleaner/internal/textnorm"
)

// Example is one labeled SMS used to train the Bayes model.
type Example struct {
	Sender string
	Body   string
	Label  string
}

// Bayes is a multinomial naive Bayes model over word unigrams, bigrams and
// sender features. It is rebuilt from scratch on every training change; with a
// few thousand SMS that takes milliseconds.
type Bayes struct {
	classCount map[string]int
	termCount  map[string]map[string]int // class -> term -> count
	totalTerms map[string]int
	vocab      map[string]struct{}
	examples   int
}

// Features turns one SMS into model features.
func Features(sender, body string) []string {
	toks := textnorm.Tokens(textnorm.Text(body))
	out := make([]string, 0, len(toks)*2+3)
	out = append(out, toks...)
	for i := 0; i+1 < len(toks); i++ {
		out = append(out, toks[i]+" "+toks[i+1])
	}
	s := textnorm.Sender(sender)
	kind := textnorm.SenderKind(s)
	out = append(out, "§kind:"+kind)
	switch kind {
	case textnorm.KindAdLine, textnorm.KindShort, textnorm.KindPhone:
		p := s
		if len(p) > 4 {
			p = p[:4]
		}
		out = append(out, "§prefix:"+p)
	case textnorm.KindAlpha:
		out = append(out, "§id:"+s)
	}
	return out
}

// TrainBayes builds a model from labeled examples. Unknown labels are ignored.
func TrainBayes(ex []Example) *Bayes {
	b := &Bayes{
		classCount: map[string]int{},
		termCount:  map[string]map[string]int{},
		totalTerms: map[string]int{},
		vocab:      map[string]struct{}{},
	}
	for _, e := range ex {
		if !IsLabel(e.Label) {
			continue
		}
		b.examples++
		b.classCount[e.Label]++
		tc := b.termCount[e.Label]
		if tc == nil {
			tc = map[string]int{}
			b.termCount[e.Label] = tc
		}
		for _, f := range Features(e.Sender, e.Body) {
			tc[f]++
			b.totalTerms[e.Label]++
			b.vocab[f] = struct{}{}
		}
	}
	return b
}

// Examples returns the number of training examples.
func (b *Bayes) Examples() int { return b.examples }

// ClassCounts returns examples per class.
func (b *Bayes) ClassCounts() map[string]int {
	out := make(map[string]int, len(b.classCount))
	for k, v := range b.classCount {
		out[k] = v
	}
	return out
}

// Predict returns a probability per known class.
func (b *Bayes) Predict(sender, body string) map[string]float64 {
	if b == nil || b.examples == 0 || len(b.classCount) < 2 {
		return nil
	}
	feats := Features(sender, body)
	v := float64(len(b.vocab) + 1)
	logp := map[string]float64{}
	// Uniform-ish prior: the label mix of a training set says little about
	// tomorrow's inbox, so damp the class prior instead of trusting it fully.
	for c, n := range b.classCount {
		lp := 0.5 * math.Log(float64(n)/float64(b.examples))
		tc := b.termCount[c]
		den := float64(b.totalTerms[c]) + v
		for _, f := range feats {
			if _, ok := b.vocab[f]; !ok {
				continue
			}
			lp += math.Log((float64(tc[f]) + 1) / den)
		}
		logp[c] = lp
	}
	maxLP := math.Inf(-1)
	for _, lp := range logp {
		maxLP = math.Max(maxLP, lp)
	}
	sum := 0.0
	out := map[string]float64{}
	for c, lp := range logp {
		p := math.Exp(lp - maxLP)
		out[c] = p
		sum += p
	}
	for c := range out {
		out[c] /= sum
	}
	return out
}

// TermLean says which class a term points to and how strongly
// (log-likelihood ratio against the other classes).
type TermLean struct {
	Term     string  `json:"term"`
	Category string  `json:"category,omitempty"`
	Strength float64 `json:"strength"`
	Seen     int     `json:"seen"`
}

// Lean computes TermLean for a term (a token or bigram).
func (b *Bayes) Lean(term string) TermLean {
	tl := TermLean{Term: term}
	if b == nil || b.examples == 0 {
		return tl
	}
	v := float64(len(b.vocab) + 1)
	best, bestLR := "", math.Inf(-1)
	for c := range b.classCount {
		tl.Seen += b.termCount[c][term]
		pIn := (float64(b.termCount[c][term]) + 1) / (float64(b.totalTerms[c]) + v)
		var otherCount, otherTotal float64
		for o := range b.classCount {
			if o == c {
				continue
			}
			otherCount += float64(b.termCount[o][term])
			otherTotal += float64(b.totalTerms[o])
		}
		pOut := (otherCount + 1) / (otherTotal + v)
		lr := math.Log(pIn / pOut)
		if lr > bestLR {
			best, bestLR = c, lr
		}
	}
	if tl.Seen > 0 && bestLR > 0.5 {
		tl.Category = best
		tl.Strength = math.Round(bestLR*100) / 100
	}
	return tl
}

// TopTerms returns the most class-indicative terms per class, for the UI.
func (b *Bayes) TopTerms(class string, n int) []TermLean {
	if b == nil {
		return nil
	}
	var out []TermLean
	for term, cnt := range b.termCount[class] {
		if cnt < 2 || len(term) > 0 && term[0] == "§"[0] {
			continue
		}
		tl := b.Lean(term)
		if tl.Category == class {
			out = append(out, tl)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		si := out[i].Strength * math.Log1p(float64(out[i].Seen))
		sj := out[j].Strength * math.Log1p(float64(out[j].Seen))
		return si > sj
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
