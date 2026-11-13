// Package engine runs one cleaning pass: list inbox conversations, skip
// anything personal, classify messages, decide a per-conversation plan and
// (when not dry) apply it on the phone.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/ahbanavi/sms-cleaner/internal/classify"
	"github.com/ahbanavi/sms-cleaner/internal/gm"
	"github.com/ahbanavi/sms-cleaner/internal/store"
	"github.com/ahbanavi/sms-cleaner/internal/textnorm"
)

// Plans for a conversation.
const (
	PlanLeave   = "leave"   // nothing to do (human, unknown, or skipped)
	PlanBlock   = "block"   // spam: block + report, then delete the conversation
	PlanDelete  = "delete"  // all offers/spam: delete the conversation (copies kept)
	PlanArchive = "archive" // service notices: archive (offer/spam messages inside are deleted first)
	PlanPartial = "partial" // delete offer/spam messages, leave the rest in the inbox
)

// Settings keys (stored in the DB, editable in the UI).
const (
	SetMode         = "mode"           // "dry" | "live"
	SetInterval     = "interval_min"   // minutes between scheduled runs, 0 = off
	SetMaxActions   = "max_actions"    // phone operations per run
	SetMaxConvs     = "max_convs"      // inbox conversations scanned per run
	SetMsgsPerConv  = "msgs_per_conv"  // latest messages fetched per conversation
	SetMinScore     = "min_score"      // classifier threshold
	SetSpamMinScore = "spam_min_score" // spam threshold
)

// Defaults for settings.
var Defaults = map[string]string{
	SetMode: "dry", SetInterval: "0", SetMaxActions: "50", SetMaxConvs: "500",
	SetMsgsPerConv: "30", SetMinScore: "2", SetSpamMinScore: "3",
}

// Notifier receives a summary after each run.
type Notifier interface {
	Notify(ctx context.Context, text string) error
}

// Engine owns the classifier and serializes runs.
type Engine struct {
	Store    *store.Store
	Connect  func(ctx context.Context) (gm.Backend, error)
	Notifier Notifier
	Log      zerolog.Logger
	// Paired reports whether a phone session exists; the scheduler waits until it does.
	Paired func() bool

	cls      atomic.Pointer[classify.Classifier]
	runMu    sync.Mutex
	running  atomic.Bool
	progress atomic.Value // string
}

// Setting reads a setting with its default.
func (e *Engine) Setting(ctx context.Context, key string) string {
	return e.Store.GetSetting(ctx, key, Defaults[key])
}

func (e *Engine) intSetting(ctx context.Context, key string) int {
	n, err := strconv.Atoi(e.Setting(ctx, key))
	if err != nil {
		n, _ = strconv.Atoi(Defaults[key])
	}
	return n
}

func (e *Engine) floatSetting(ctx context.Context, key string) float64 {
	f, err := strconv.ParseFloat(e.Setting(ctx, key), 64)
	if err != nil {
		f, _ = strconv.ParseFloat(Defaults[key], 64)
	}
	return f
}

// Init seeds rules on first start and builds the classifier.
func (e *Engine) Init(ctx context.Context) error {
	if err := e.Store.CloseInterruptedRuns(ctx); err != nil {
		return err
	}
	if e.Store.RuleCount(ctx) == 0 {
		for _, r := range classify.SeedRules() {
			if _, err := e.Store.AddRule(ctx, r, true); err != nil {
				e.Log.Warn().Err(err).Str("pattern", r.Pattern).Msg("seed rule rejected")
			}
		}
	}
	return e.Rebuild(ctx)
}

// Rebuild reloads rules + training data into a fresh classifier.
func (e *Engine) Rebuild(ctx context.Context) error {
	rules, err := e.Store.ListRules(ctx)
	if err != nil {
		return err
	}
	ex, err := e.Store.Examples(ctx)
	if err != nil {
		return err
	}
	cfg := classify.Config{MinScore: e.floatSetting(ctx, SetMinScore), SpamMinScore: e.floatSetting(ctx, SetSpamMinScore)}
	e.cls.Store(classify.New(cfg, rules, ex))
	return nil
}

// Classifier returns the current classifier.
func (e *Engine) Classifier() *classify.Classifier { return e.cls.Load() }

// Reclassify re-scores every stored message with the current classifier.
func (e *Engine) Reclassify(ctx context.Context) (int, error) {
	c := e.Classifier()
	n := 0
	err := e.Store.AllForReclassify(ctx, func(id, sender, body string) error {
		n++
		return e.Store.UpdateClassification(ctx, id, c.Classify(sender, body))
	})
	return n, err
}

// Running reports whether a run is in progress, with a progress note.
func (e *Engine) Running() (bool, string) {
	p, _ := e.progress.Load().(string)
	return e.running.Load(), p
}

// ErrBusy is returned when a run is already in progress.
var ErrBusy = errors.New("a run is already in progress")

// Report summarizes a run.
type Report struct {
	RunID int64          `json:"run_id"`
	Dry   bool           `json:"dry"`
	Stats map[string]int `json:"stats"`
}

// Run performs one cleaning pass. dry=true decides and logs but changes nothing.
func (e *Engine) Run(ctx context.Context, dry bool, trigger string) (rep Report, err error) {
	if !e.runMu.TryLock() {
		return rep, ErrBusy
	}
	defer e.runMu.Unlock()
	e.running.Store(true)
	defer e.running.Store(false)
	e.progress.Store("connecting to phone")

	stats := map[string]int{}
	runID, err := e.Store.StartRun(ctx, dry, trigger)
	if err != nil {
		return rep, err
	}
	rep = Report{RunID: runID, Dry: dry, Stats: stats}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
		_ = e.Store.FinishRun(context.WithoutCancel(ctx), runID, stats, err)
		e.progress.Store("")
		e.notify(context.WithoutCancel(ctx), rep, err)
	}()

	be, err := e.Connect(ctx)
	if err != nil {
		return rep, err
	}
	defer be.Close()

	e.progress.Store("loading contacts")
	contacts, err := be.Contacts(ctx)
	if err != nil {
		// Without the contact list we cannot guarantee contacts stay untouched.
		return rep, fmt.Errorf("load contacts (refusing to continue without them): %w", err)
	}
	contactKeys := map[string]bool{}
	for _, c := range contacts {
		if k := textnorm.PhoneKey(c.Number); len(k) >= 5 {
			contactKeys[k] = true
		}
	}
	stats["contacts"] = len(contactKeys)

	e.progress.Store("listing inbox")
	convs, err := be.Conversations(ctx, gm.FolderInbox, e.intSetting(ctx, SetMaxConvs))
	if err != nil {
		return rep, fmt.Errorf("list conversations: %w", err)
	}
	stats["conversations"] = len(convs)

	r := &runner{e: e, be: be, runID: runID, dry: dry, stats: stats, budget: e.intSetting(ctx, SetMaxActions), cls: e.Classifier()}
	for i, conv := range convs {
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		e.progress.Store(fmt.Sprintf("conversation %d/%d", i+1, len(convs)))
		if err := r.conversation(ctx, conv, contactKeys); err != nil {
			stats["errors"]++
			e.Log.Warn().Err(err).Str("conv", conv.ID).Msg("conversation failed")
		}
	}
	return rep, nil
}

type runner struct {
	e      *Engine
	be     gm.Backend
	cls    *classify.Classifier
	runID  int64
	dry    bool
	stats  map[string]int
	budget int
}

func (r *runner) conversation(ctx context.Context, conv gm.Conversation, contactKeys map[string]bool) error {
	st := r.e.Store
	sender := textnorm.Sender(gm.FirstNonEmpty(conv.Phone(), conv.Name))
	row := store.Conversation{ID: conv.ID, Name: conv.Name, Sender: sender, SenderKind: textnorm.SenderKind(sender), Folder: string(gm.FolderInbox), LastTS: conv.LastTS.Unix(), Plan: PlanLeave}

	// Safety gates. Contacts are never even read.
	switch {
	case conv.IsGroup:
		row.SkipReason = "group"
	case isContact(conv, contactKeys):
		row.SkipReason = "contact"
	case conv.Pinned:
		row.SkipReason = "pinned"
	}
	if row.SkipReason != "" {
		r.stats["skipped_"+row.SkipReason]++
		return st.UpsertConversation(ctx, row)
	}

	prev, _ := st.ConversationMessages(ctx, conv.ID)
	var msgs []store.Message
	if len(prev) > 0 && lastTS(ctx, st, conv.ID) == row.LastTS {
		// Nothing new since last scan: re-plan from the stored copy.
		for _, m := range prev {
			if m.OnPhone {
				msgs = append(msgs, m)
			}
		}
		for i := range msgs {
			res := r.cls.Classify(msgs[i].Sender, msgs[i].Body)
			msgs[i].Category, msgs[i].Confidence, msgs[i].Reasons = res.Category, res.Confidence, res.Reasons
			_ = st.UpdateClassification(ctx, msgs[i].ID, res)
		}
		r.stats["cached_convs"]++
	} else {
		live, err := r.be.Messages(ctx, conv, r.e.intSetting(ctx, SetMsgsPerConv))
		if err != nil {
			return fmt.Errorf("fetch messages: %w", err)
		}
		for _, m := range live {
			if m.FromMe {
				row.SkipReason = "replied"
				r.stats["skipped_replied"]++
				return st.UpsertConversation(ctx, row)
			}
		}
		labels := map[string]string{}
		for _, p := range prev {
			labels[p.ID] = p.Label
		}
		for _, m := range live {
			body := m.Body
			if body == "" && m.HasMedia {
				body = "[media]"
			}
			sm := store.Message{ID: m.ID, ConvID: conv.ID, Sender: textnorm.Sender(gm.FirstNonEmpty(m.Sender, sender)), SenderName: m.SenderName, Body: body, TS: m.TS.Unix(), Label: labels[m.ID], OnPhone: true}
			res := r.cls.Classify(sm.Sender, sm.Body)
			if m.HasMedia && m.Body == "" {
				res = classify.Result{Category: classify.Unknown}
			}
			sm.Category, sm.Confidence, sm.Reasons = res.Category, res.Confidence, res.Reasons
			if err := st.UpsertMessage(ctx, sm, res); err != nil {
				return err
			}
			if !r.dry {
				st.BumpRuleHits(ctx, res.MatchingRules())
			}
			msgs = append(msgs, sm)
		}
		r.stats["scanned_messages"] += len(msgs)
	}

	plan, cat, junk := decide(msgs)
	row.Plan, row.Category = plan, cat
	r.stats["plan_"+plan]++
	if err := st.UpsertConversation(ctx, row); err != nil {
		return err
	}
	if plan == PlanLeave {
		for _, m := range msgs {
			if m.Effective() == classify.Unknown {
				r.stats["unknown_messages"]++
			}
		}
		return nil
	}
	return r.apply(ctx, conv, row, plan, junk, msgs)
}

func lastTS(ctx context.Context, st *store.Store, convID string) int64 {
	var ts int64
	_ = st.DB.QueryRowContext(ctx, `SELECT last_ts FROM conversations WHERE id=?`, convID).Scan(&ts)
	return ts
}

func isContact(conv gm.Conversation, keys map[string]bool) bool {
	for _, p := range conv.Participants {
		if p.ContactID != "" {
			return true
		}
		if k := textnorm.PhoneKey(p.Number); len(k) >= 5 && keys[k] {
			return true
		}
	}
	return false
}

// decide picks a plan from the effective categories of a conversation's
// messages. junk are the offer/spam messages to delete individually.
func decide(msgs []store.Message) (plan, category string, junk []store.Message) {
	if len(msgs) == 0 {
		return PlanLeave, "", nil
	}
	n := len(msgs)
	count := map[string]int{}
	for _, m := range msgs {
		count[m.Effective()]++
		if c := m.Effective(); c == classify.Offer || c == classify.Spam {
			junk = append(junk, m)
		}
	}
	dominant := classify.Unknown
	for _, c := range []string{classify.Archive, classify.Offer, classify.Spam, classify.Keep} {
		if count[c] > count[dominant] {
			dominant = c
		}
	}
	if count[classify.Keep] > 0 {
		return PlanLeave, classify.Keep, nil
	}
	if count[classify.Spam] == n || (count[classify.Spam]*2 > n && count[classify.Archive] == 0 && count[classify.Unknown] == 0) {
		return PlanBlock, classify.Spam, junk
	}
	rest := count[classify.Archive] + count[classify.Unknown]
	switch {
	case rest == 0:
		return PlanDelete, dominant, junk
	case count[classify.Unknown] == 0:
		return PlanArchive, classify.Archive, junk
	case len(junk) > 0:
		return PlanPartial, dominant, junk
	default:
		return PlanLeave, dominant, nil
	}
}

func (r *runner) apply(ctx context.Context, conv gm.Conversation, row store.Conversation, plan string, junk []store.Message, msgs []store.Message) error {
	st := r.e.Store
	do := func(action, msgID, detail string, fn func() error) bool {
		a := store.Action{RunID: r.runID, ConvID: conv.ID, MsgID: msgID, Sender: row.Sender, Category: row.Category, Action: action, Dry: r.dry, OK: true, Detail: detail}
		if !r.dry {
			if r.budget <= 0 {
				a.OK, a.Error = false, "skipped: max actions per run reached"
				r.stats["capped"]++
				_ = st.LogAction(ctx, a)
				return false
			}
			r.budget--
			if err := fn(); err != nil {
				a.OK, a.Error = false, err.Error()
				r.stats["errors"]++
				_ = st.LogAction(ctx, a)
				return false
			}
		}
		r.stats["action_"+action]++
		_ = st.LogAction(ctx, a)
		return true
	}
	preview := func(m store.Message) string {
		b := []rune(m.Body)
		if len(b) > 80 {
			b = append(b[:80], '…')
		}
		return string(b)
	}

	switch plan {
	case PlanBlock:
		if do("block_report", "", fmt.Sprintf("%d messages", len(msgs)), func() error { return r.be.BlockAndReport(ctx, conv) }) &&
			do("delete_conversation", "", "after block", func() error { return r.be.DeleteConversation(ctx, conv) }) && !r.dry {
			return st.MarkConversationAction(ctx, conv.ID, "spam-deleted", false)
		}
	case PlanDelete:
		if do("delete_conversation", "", fmt.Sprintf("%d %s messages", len(msgs), row.Category), func() error { return r.be.DeleteConversation(ctx, conv) }) && !r.dry {
			return st.MarkConversationAction(ctx, conv.ID, "deleted", false)
		}
	case PlanArchive, PlanPartial:
		for _, m := range junk {
			if do("delete_message", m.ID, m.Effective()+": "+preview(m), func() error { return r.be.DeleteMessage(ctx, m.ID) }) && !r.dry {
				_ = st.MarkAction(ctx, m.ID, "deleted", false)
			}
		}
		if plan == PlanArchive {
			if do("archive", "", fmt.Sprintf("%d messages", len(msgs)-len(junk)), func() error { return r.be.Archive(ctx, conv) }) && !r.dry {
				return st.MarkConversationAction(ctx, conv.ID, "archived", true)
			}
		}
	}
	return nil
}

func (e *Engine) notify(ctx context.Context, rep Report, err error) {
	if e.Notifier == nil {
		return
	}
	s := rep.Stats
	acted := s["action_block_report"] + s["action_delete_conversation"] + s["action_delete_message"] + s["action_archive"]
	if err == nil && (rep.Dry || (acted == 0 && s["errors"] == 0)) {
		return
	}
	var text string
	if err != nil {
		text = "⚠️ <b>SMS cleaner run failed</b>\n" + htmlEscape(err.Error())
	} else {
		text = fmt.Sprintf("🧹 <b>SMS cleaner</b>\n🚫 spam blocked: %d\n🏷 offers removed: %d convs, %d msgs\n🗄 archived: %d\n❓ need review: %d",
			s["action_block_report"], s["action_delete_conversation"]-s["action_block_report"], s["action_delete_message"], s["action_archive"], s["unknown_messages"])
		if s["capped"] > 0 {
			text += fmt.Sprintf("\n⏸ capped: %d actions left for next run", s["capped"])
		}
		if s["errors"] > 0 {
			text += fmt.Sprintf("\n⚠️ errors: %d", s["errors"])
		}
	}
	if nerr := e.Notifier.Notify(ctx, text); nerr != nil {
		e.Log.Warn().Err(nerr).Msg("notify failed")
	}
}

func htmlEscape(s string) string {
	r := []rune(s)
	out := make([]rune, 0, len(r))
	for _, c := range r {
		switch c {
		case '<':
			out = append(out, []rune("&lt;")...)
		case '>':
			out = append(out, []rune("&gt;")...)
		case '&':
			out = append(out, []rune("&amp;")...)
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

// Scheduler runs Run on the configured interval until ctx ends.
func (e *Engine) Scheduler(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		next := e.NextRun(ctx)
		if next.IsZero() || time.Now().Before(next) {
			continue
		}
		dry := e.Setting(ctx, SetMode) != "live"
		if _, err := e.Run(ctx, dry, "schedule"); err != nil && !errors.Is(err, ErrBusy) {
			e.Log.Err(err).Msg("scheduled run failed")
		}
	}
}

// NextRun returns when the scheduler will run next (zero if disabled).
func (e *Engine) NextRun(ctx context.Context) time.Time {
	iv := e.intSetting(ctx, SetInterval)
	if iv <= 0 || (e.Paired != nil && !e.Paired()) {
		return time.Time{}
	}
	runs, _ := e.Store.ListRuns(ctx, 20)
	for _, r := range runs {
		if r.Trigger == "schedule" || r.Trigger == "manual" {
			return time.Unix(r.StartedAt, 0).Add(time.Duration(iv) * time.Minute)
		}
	}
	return time.Now()
}
