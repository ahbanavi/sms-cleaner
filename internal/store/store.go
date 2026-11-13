// Package store keeps everything in one SQLite file: scanned messages (with a
// copy of every deleted one), labels, rules, runs and the action log.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ahbanavi/sms-cleaner/internal/classify"
	"github.com/ahbanavi/sms-cleaner/internal/textnorm"
)

const schema = `
CREATE TABLE IF NOT EXISTS conversations (
	id           TEXT PRIMARY KEY,
	name         TEXT NOT NULL DEFAULT '',
	sender       TEXT NOT NULL DEFAULT '',
	sender_kind  TEXT NOT NULL DEFAULT '',
	folder       TEXT NOT NULL DEFAULT 'inbox',
	skip_reason  TEXT NOT NULL DEFAULT '',
	category     TEXT NOT NULL DEFAULT '',
	plan         TEXT NOT NULL DEFAULT '',
	last_ts      INTEGER NOT NULL DEFAULT 0,
	updated_at   INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS messages (
	id          TEXT PRIMARY KEY,
	conv_id     TEXT NOT NULL,
	sender      TEXT NOT NULL DEFAULT '',
	sender_name TEXT NOT NULL DEFAULT '',
	body        TEXT NOT NULL DEFAULT '',
	norm        TEXT NOT NULL DEFAULT '',
	ts          INTEGER NOT NULL DEFAULT 0,
	category    TEXT NOT NULL DEFAULT '',
	confidence  REAL NOT NULL DEFAULT 0,
	reasons     TEXT NOT NULL DEFAULT '[]',
	label       TEXT NOT NULL DEFAULT '',
	label_at    INTEGER NOT NULL DEFAULT 0,
	action      TEXT NOT NULL DEFAULT '',
	acted_at    INTEGER NOT NULL DEFAULT 0,
	on_phone    INTEGER NOT NULL DEFAULT 1,
	seen_at     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS messages_conv ON messages(conv_id, ts);
CREATE INDEX IF NOT EXISTS messages_cat ON messages(category, ts);
CREATE INDEX IF NOT EXISTS messages_label ON messages(label);
CREATE TABLE IF NOT EXISTS rules (
	id         INTEGER PRIMARY KEY,
	kind       TEXT NOT NULL,
	pattern    TEXT NOT NULL,
	category   TEXT NOT NULL,
	weight     REAL NOT NULL DEFAULT 2,
	source     TEXT NOT NULL DEFAULT 'user',
	enabled    INTEGER NOT NULL DEFAULT 1,
	note       TEXT NOT NULL DEFAULT '',
	hits       INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL DEFAULT 0,
	UNIQUE(kind, pattern)
);
CREATE TABLE IF NOT EXISTS training (
	id         INTEGER PRIMARY KEY,
	sender     TEXT NOT NULL DEFAULT '',
	body       TEXT NOT NULL,
	label      TEXT NOT NULL,
	source     TEXT NOT NULL DEFAULT 'import',
	created_at INTEGER NOT NULL DEFAULT 0,
	UNIQUE(sender, body)
);
CREATE TABLE IF NOT EXISTS runs (
	id          INTEGER PRIMARY KEY,
	started_at  INTEGER NOT NULL,
	finished_at INTEGER NOT NULL DEFAULT 0,
	dry         INTEGER NOT NULL DEFAULT 1,
	trigger     TEXT NOT NULL DEFAULT '',
	stats       TEXT NOT NULL DEFAULT '{}',
	error       TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS actions (
	id       INTEGER PRIMARY KEY,
	run_id   INTEGER NOT NULL,
	conv_id  TEXT NOT NULL,
	msg_id   TEXT NOT NULL DEFAULT '',
	sender   TEXT NOT NULL DEFAULT '',
	category TEXT NOT NULL DEFAULT '',
	action   TEXT NOT NULL,
	dry      INTEGER NOT NULL DEFAULT 1,
	ok       INTEGER NOT NULL DEFAULT 1,
	error    TEXT NOT NULL DEFAULT '',
	detail   TEXT NOT NULL DEFAULT '',
	at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS actions_run ON actions(run_id);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// Store wraps the SQLite database.
type Store struct{ DB *sql.DB }

// Open opens (and migrates) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer; keeps things simple and safe
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{DB: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

func now() int64 { return time.Now().Unix() }

// ---- settings ----

// GetSetting returns a setting or def.
func (s *Store) GetSetting(ctx context.Context, key, def string) string {
	var v string
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

// SetSetting stores a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// ---- conversations & messages ----

// Conversation row.
type Conversation struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Sender     string `json:"sender"`
	SenderKind string `json:"sender_kind"`
	Folder     string `json:"folder"`
	SkipReason string `json:"skip_reason,omitempty"`
	Category   string `json:"category"`
	Plan       string `json:"plan"`
	LastTS     int64  `json:"last_ts"`
}

// UpsertConversation records a scanned conversation.
func (s *Store) UpsertConversation(ctx context.Context, c Conversation) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO conversations(id,name,sender,sender_kind,folder,skip_reason,category,plan,last_ts,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, sender=excluded.sender, sender_kind=excluded.sender_kind,
		folder=excluded.folder, skip_reason=excluded.skip_reason, category=excluded.category, plan=excluded.plan,
		last_ts=excluded.last_ts, updated_at=excluded.updated_at`,
		c.ID, c.Name, c.Sender, c.SenderKind, c.Folder, c.SkipReason, c.Category, c.Plan, c.LastTS, now())
	return err
}

// Message row.
type Message struct {
	ID         string            `json:"id"`
	ConvID     string            `json:"conv_id"`
	Sender     string            `json:"sender"`
	SenderName string            `json:"sender_name"`
	Body       string            `json:"body"`
	TS         int64             `json:"ts"`
	Category   string            `json:"category"`
	Confidence float64           `json:"confidence"`
	Reasons    []classify.Reason `json:"reasons"`
	Label      string            `json:"label"`
	Action     string            `json:"action"`
	ActedAt    int64             `json:"acted_at"`
	OnPhone    bool              `json:"on_phone"`
}

// Effective is the user label when set, else the predicted category.
func (m Message) Effective() string {
	if m.Label != "" {
		return m.Label
	}
	return m.Category
}

// UpsertMessage stores a scanned message and its current classification. It
// never overwrites a user label or a recorded action.
func (s *Store) UpsertMessage(ctx context.Context, m Message, res classify.Result) error {
	reasons, _ := json.Marshal(res.Reasons)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO messages(id,conv_id,sender,sender_name,body,norm,ts,category,confidence,reasons,seen_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET category=excluded.category, confidence=excluded.confidence, reasons=excluded.reasons,
		sender_name=excluded.sender_name, on_phone=CASE WHEN messages.action IN ('deleted','spam-deleted') THEN 0 ELSE 1 END`,
		m.ID, m.ConvID, m.Sender, m.SenderName, m.Body, textnorm.Text(m.Body), m.TS, res.Category, res.Confidence, string(reasons), now())
	return err
}

// MarkAction records what was done to a message.
func (s *Store) MarkAction(ctx context.Context, msgID, action string, onPhone bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE messages SET action=?, acted_at=?, on_phone=? WHERE id=?`, action, now(), boolInt(onPhone), msgID)
	return err
}

// MarkConversationAction records an action for every stored message of a
// conversation that is still on the phone.
func (s *Store) MarkConversationAction(ctx context.Context, convID, action string, onPhone bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE messages SET action=?, acted_at=?, on_phone=? WHERE conv_id=? AND on_phone=1`, action, now(), boolInt(onPhone), convID)
	return err
}

// SetLabel sets (or clears with "") the user label of a message.
func (s *Store) SetLabel(ctx context.Context, msgID, label string) error {
	if label != "" && !classify.IsLabel(label) {
		return fmt.Errorf("bad label %q", label)
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE messages SET label=?, label_at=? WHERE id=?`, label, now(), msgID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// MessageFilter selects messages for the UI.
type MessageFilter struct {
	Category string // predicted or labeled category; "unlabeled" special
	Label    string // "", "any", "none", or a label
	Action   string // "", "none", "any", or an action
	Sender   string
	Query    string
	From, To int64
	OnPhone  string // "", "yes", "no"
	Limit    int
	Offset   int
}

// ListMessages returns messages matching f, newest first, with the total count.
func (s *Store) ListMessages(ctx context.Context, f MessageFilter) ([]Message, int, error) {
	var where []string
	var args []any
	if f.Category != "" {
		where = append(where, `(CASE WHEN label<>'' THEN label ELSE category END)=?`)
		args = append(args, f.Category)
	}
	switch f.Label {
	case "":
	case "any":
		where = append(where, `label<>''`)
	case "none":
		where = append(where, `label=''`)
	default:
		where = append(where, `label=?`)
		args = append(args, f.Label)
	}
	switch f.Action {
	case "":
	case "none":
		where = append(where, `action=''`)
	case "any":
		where = append(where, `action<>''`)
	default:
		where = append(where, `action=?`)
		args = append(args, f.Action)
	}
	switch f.OnPhone {
	case "yes":
		where = append(where, `on_phone=1`)
	case "no":
		where = append(where, `on_phone=0`)
	}
	if f.Sender != "" {
		where = append(where, `sender=?`)
		args = append(args, f.Sender)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		for _, term := range strings.Fields(textnorm.Text(q)) {
			where = append(where, `(norm LIKE ? ESCAPE '\' OR sender LIKE ? ESCAPE '\' OR sender_name LIKE ? ESCAPE '\')`)
			like := "%" + escapeLike(term) + "%"
			args = append(args, like, like, like)
		}
	}
	if f.From > 0 {
		where = append(where, `ts>=?`)
		args = append(args, f.From)
	}
	if f.To > 0 {
		where = append(where, `ts<?`)
		args = append(args, f.To)
	}
	w := ""
	if len(where) > 0 {
		w = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,conv_id,sender,sender_name,body,ts,category,confidence,reasons,label,action,acted_at,on_phone
		FROM messages`+w+` ORDER BY ts DESC LIMIT ? OFFSET ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

// GetMessage loads one message.
func (s *Store) GetMessage(ctx context.Context, id string) (Message, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id,conv_id,sender,sender_name,body,ts,category,confidence,reasons,label,action,acted_at,on_phone FROM messages WHERE id=?`, id)
	return scanMessage(row)
}

// ConversationMessages loads all stored messages of a conversation, oldest first.
func (s *Store) ConversationMessages(ctx context.Context, convID string) ([]Message, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,conv_id,sender,sender_name,body,ts,category,confidence,reasons,label,action,acted_at,on_phone FROM messages WHERE conv_id=? ORDER BY ts`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanMessage(r scanner) (Message, error) {
	var m Message
	var reasons string
	var onPhone int
	err := r.Scan(&m.ID, &m.ConvID, &m.Sender, &m.SenderName, &m.Body, &m.TS, &m.Category, &m.Confidence, &reasons, &m.Label, &m.Action, &m.ActedAt, &onPhone)
	if err != nil {
		return m, err
	}
	m.OnPhone = onPhone == 1
	_ = json.Unmarshal([]byte(reasons), &m.Reasons)
	return m, nil
}

// AllForReclassify streams every stored message (id, sender, body).
func (s *Store) AllForReclassify(ctx context.Context, fn func(id, sender, body string) error) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,sender,body FROM messages`)
	if err != nil {
		return err
	}
	type row struct{ id, sender, body string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.sender, &r.body); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	rows.Close()
	for _, r := range all {
		if err := fn(r.id, r.sender, r.body); err != nil {
			return err
		}
	}
	return nil
}

// UpdateClassification rewrites the predicted category of a stored message.
func (s *Store) UpdateClassification(ctx context.Context, id string, res classify.Result) error {
	reasons, _ := json.Marshal(res.Reasons)
	_, err := s.DB.ExecContext(ctx, `UPDATE messages SET category=?, confidence=?, reasons=? WHERE id=?`, res.Category, res.Confidence, string(reasons), id)
	return err
}

// Senders lists distinct senders for a category with counts (for filters).
func (s *Store) Senders(ctx context.Context, category string) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT sender, MAX(sender_name), COUNT(*) FROM messages
		WHERE (CASE WHEN label<>'' THEN label ELSE category END)=? GROUP BY sender ORDER BY COUNT(*) DESC LIMIT 300`, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var sender, name string
		var n int
		if err := rows.Scan(&sender, &name, &n); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"sender": sender, "name": name, "count": n})
	}
	return out, rows.Err()
}

// ---- rules ----

// ListRules returns all rules.
func (s *Store) ListRules(ctx context.Context) ([]classify.Rule, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,kind,pattern,category,weight,source,enabled,note,hits FROM rules ORDER BY category, kind, pattern`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []classify.Rule
	for rows.Next() {
		var r classify.Rule
		var en int
		if err := rows.Scan(&r.ID, &r.Kind, &r.Pattern, &r.Category, &r.Weight, &r.Source, &en, &r.Note, &r.Hits); err != nil {
			return nil, err
		}
		r.Enabled = en == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// ErrDuplicate is returned when a rule with the same kind+pattern exists.
var ErrDuplicate = errors.New("a rule with this pattern already exists")

// AddRule validates and inserts a rule. With replace, an existing rule with
// the same kind+pattern is updated instead.
func (s *Store) AddRule(ctx context.Context, r classify.Rule, replace bool) (int64, error) {
	if err := r.Validate(); err != nil {
		return 0, err
	}
	if r.Source == "" {
		r.Source = "user"
	}
	q := `INSERT INTO rules(kind,pattern,category,weight,source,enabled,note,created_at) VALUES(?,?,?,?,?,1,?,?)`
	if replace {
		q += ` ON CONFLICT(kind,pattern) DO UPDATE SET category=excluded.category, weight=excluded.weight, source=excluded.source, note=excluded.note, enabled=1`
	}
	res, err := s.DB.ExecContext(ctx, q, r.Kind, r.Pattern, r.Category, r.Weight, r.Source, r.Note, now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrDuplicate
		}
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// UpdateRule changes a rule's category, weight, enabled flag or note.
func (s *Store) UpdateRule(ctx context.Context, r classify.Rule) error {
	if err := r.Validate(); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE rules SET kind=?, pattern=?, category=?, weight=?, enabled=?, note=? WHERE id=?`,
		r.Kind, r.Pattern, r.Category, r.Weight, boolInt(r.Enabled), r.Note, r.ID)
	return err
}

// DeleteRule removes a rule.
func (s *Store) DeleteRule(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM rules WHERE id=?`, id)
	return err
}

// RuleCount returns the number of rules.
func (s *Store) RuleCount(ctx context.Context) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM rules`).Scan(&n)
	return n
}

// BumpRuleHits increments hit counters.
func (s *Store) BumpRuleHits(ctx context.Context, ids []int64) {
	for _, id := range ids {
		_, _ = s.DB.ExecContext(ctx, `UPDATE rules SET hits=hits+1 WHERE id=?`, id)
	}
}

// ---- training ----

// AddTraining stores an imported labeled example.
func (s *Store) AddTraining(ctx context.Context, sender, body, label, source string) error {
	if !classify.IsLabel(label) {
		return fmt.Errorf("bad label %q", label)
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO training(sender,body,label,source,created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(sender,body) DO UPDATE SET label=excluded.label, source=excluded.source`, sender, body, label, source, now())
	return err
}

// Examples returns training examples: imported ones plus user-labeled messages.
func (s *Store) Examples(ctx context.Context) ([]classify.Example, error) {
	var out []classify.Example
	for _, q := range []string{
		`SELECT sender, body, label FROM training`,
		`SELECT sender, body, label FROM messages WHERE label<>''`,
	} {
		rows, err := s.DB.QueryContext(ctx, q)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var e classify.Example
			if err := rows.Scan(&e.Sender, &e.Body, &e.Label); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, e)
		}
		rows.Close()
	}
	return out, nil
}

// TrainingCount returns imported training examples per label.
func (s *Store) TrainingCount(ctx context.Context) map[string]int {
	out := map[string]int{}
	rows, err := s.DB.QueryContext(ctx, `SELECT label, COUNT(*) FROM training GROUP BY label`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var l string
		var n int
		if rows.Scan(&l, &n) == nil {
			out[l] = n
		}
	}
	return out
}

// ---- runs & actions ----

// Run row.
type Run struct {
	ID         int64          `json:"id"`
	StartedAt  int64          `json:"started_at"`
	FinishedAt int64          `json:"finished_at"`
	Dry        bool           `json:"dry"`
	Trigger    string         `json:"trigger"`
	Stats      map[string]int `json:"stats"`
	Error      string         `json:"error,omitempty"`
}

// StartRun inserts a run.
func (s *Store) StartRun(ctx context.Context, dry bool, trigger string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO runs(started_at,dry,trigger) VALUES(?,?,?)`, now(), boolInt(dry), trigger)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CloseInterruptedRuns marks runs that never finished (the server stopped
// mid-run) as failed, so they don't look like they are still going.
func (s *Store) CloseInterruptedRuns(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET finished_at=?, error='interrupted: the server stopped during this run'
		WHERE finished_at IS NULL OR finished_at=0`, now())
	return err
}

// FinishRun records the outcome of a run.
func (s *Store) FinishRun(ctx context.Context, id int64, stats map[string]int, runErr error) error {
	b, _ := json.Marshal(stats)
	e := ""
	if runErr != nil {
		e = runErr.Error()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET finished_at=?, stats=?, error=? WHERE id=?`, now(), string(b), e, id)
	return err
}

// ListRuns returns recent runs.
func (s *Store) ListRuns(ctx context.Context, limit int) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,started_at,finished_at,dry,trigger,stats,error FROM runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var dry int
		var stats string
		if err := rows.Scan(&r.ID, &r.StartedAt, &r.FinishedAt, &dry, &r.Trigger, &stats, &r.Error); err != nil {
			return nil, err
		}
		r.Dry = dry == 1
		_ = json.Unmarshal([]byte(stats), &r.Stats)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Action row.
type Action struct {
	ID       int64  `json:"id"`
	RunID    int64  `json:"run_id"`
	ConvID   string `json:"conv_id"`
	MsgID    string `json:"msg_id,omitempty"`
	Sender   string `json:"sender"`
	Category string `json:"category"`
	Action   string `json:"action"`
	Dry      bool   `json:"dry"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Detail   string `json:"detail,omitempty"`
	At       int64  `json:"at"`
}

// LogAction appends to the action log.
func (s *Store) LogAction(ctx context.Context, a Action) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO actions(run_id,conv_id,msg_id,sender,category,action,dry,ok,error,detail,at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		a.RunID, a.ConvID, a.MsgID, a.Sender, a.Category, a.Action, boolInt(a.Dry), boolInt(a.OK), a.Error, a.Detail, now())
	return err
}

// ListActions returns recent actions, optionally for one run.
func (s *Store) ListActions(ctx context.Context, runID int64, limit int) ([]Action, error) {
	q := `SELECT id,run_id,conv_id,msg_id,sender,category,action,dry,ok,error,detail,at FROM actions`
	var args []any
	if runID > 0 {
		q += ` WHERE run_id=?`
		args = append(args, runID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Action
	for rows.Next() {
		var a Action
		var dry, ok int
		if err := rows.Scan(&a.ID, &a.RunID, &a.ConvID, &a.MsgID, &a.Sender, &a.Category, &a.Action, &dry, &ok, &a.Error, &a.Detail, &a.At); err != nil {
			return nil, err
		}
		a.Dry, a.OK = dry == 1, ok == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- stats ----

// Stats summarizes the database for the dashboard.
func (s *Store) Stats(ctx context.Context) map[string]any {
	out := map[string]any{}
	byCat := map[string]int{}
	rows, err := s.DB.QueryContext(ctx, `SELECT CASE WHEN label<>'' THEN label ELSE category END, COUNT(*) FROM messages GROUP BY 1`)
	if err == nil {
		for rows.Next() {
			var c string
			var n int
			if rows.Scan(&c, &n) == nil {
				byCat[c] = n
			}
		}
		rows.Close()
	}
	out["by_category"] = byCat
	count := func(q string) int {
		var n int
		_ = s.DB.QueryRowContext(ctx, q).Scan(&n)
		return n
	}
	out["messages"] = count(`SELECT COUNT(*) FROM messages`)
	out["labeled"] = count(`SELECT COUNT(*) FROM messages WHERE label<>''`)
	out["offers_saved"] = count(`SELECT COUNT(*) FROM messages WHERE (CASE WHEN label<>'' THEN label ELSE category END)='offer'`)
	out["deleted"] = count(`SELECT COUNT(*) FROM messages WHERE on_phone=0`)
	out["archived"] = count(`SELECT COUNT(*) FROM messages WHERE action='archived'`)
	out["blocked"] = count(`SELECT COUNT(DISTINCT conv_id) FROM messages WHERE action='spam-deleted'`)
	out["review"] = count(`SELECT COUNT(*) FROM messages WHERE category='unknown' AND label='' AND on_phone=1`)
	out["conversations"] = count(`SELECT COUNT(*) FROM conversations`)
	out["skipped_contacts"] = count(`SELECT COUNT(*) FROM conversations WHERE skip_reason='contact'`)
	out["rules"] = count(`SELECT COUNT(*) FROM rules`)
	out["training"] = count(`SELECT COUNT(*) FROM training`)
	return out
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
