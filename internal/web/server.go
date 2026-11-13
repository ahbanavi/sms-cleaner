// Package web serves the JSON API and the embedded single-page UI.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/ahbanavi/sms-cleaner/internal/classify"
	"github.com/ahbanavi/sms-cleaner/internal/engine"
	"github.com/ahbanavi/sms-cleaner/internal/gm"
	"github.com/ahbanavi/sms-cleaner/internal/store"
	"github.com/ahbanavi/sms-cleaner/internal/textnorm"
)

//go:embed static
var static embed.FS

// Server wires HTTP to the engine.
type Server struct {
	Engine      *engine.Engine
	Store       *store.Store
	Pairer      *gm.Pairer
	Keeper      *gm.Keeper
	SessionPath string
	Demo        bool
	User, Pass  string
	Log         zerolog.Logger
	Import      func(ctx context.Context, name string, r io.Reader) (string, error)
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	files := http.FileServerFS(sub)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Embedded files carry no modification time; make browsers revalidate
		// so a new binary's UI shows up without a hard refresh.
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))

	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("POST /api/run", s.run)
	mux.HandleFunc("GET /api/messages", s.listMessages)
	mux.HandleFunc("GET /api/messages/{id}", s.getMessage)
	mux.HandleFunc("POST /api/messages/{id}/label", s.labelMessage)
	mux.HandleFunc("GET /api/conversations/{id}", s.conversation)
	mux.HandleFunc("POST /api/reclassify", s.reclassify)
	mux.HandleFunc("GET /api/senders", s.senders)
	mux.HandleFunc("GET /api/rules", s.listRules)
	mux.HandleFunc("POST /api/rules", s.addRule)
	mux.HandleFunc("PUT /api/rules/{id}", s.updateRule)
	mux.HandleFunc("DELETE /api/rules/{id}", s.deleteRule)
	mux.HandleFunc("POST /api/test", s.test)
	mux.HandleFunc("GET /api/runs", s.runs)
	mux.HandleFunc("GET /api/actions", s.actions)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("GET /api/model", s.model)
	mux.HandleFunc("GET /api/pair", s.pairState)
	mux.HandleFunc("POST /api/pair/start", s.pairStart)
	mux.HandleFunc("POST /api/pair/google", s.pairGoogle)
	mux.HandleFunc("POST /api/pair/cancel", s.pairCancel)
	mux.HandleFunc("POST /api/unpair", s.unpair)
	mux.HandleFunc("POST /api/import", s.importFile)
	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	root.Handle("/", s.auth(securityHeaders(mux)))
	return root
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) auth(h http.Handler) http.Handler {
	if s.Pass == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(s.User)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(s.Pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="sms-cleaner"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func (s *Server) paired() bool {
	if s.Demo {
		return true
	}
	_, err := gm.LoadSession(s.SessionPath)
	return err == nil
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	running, progress := s.Engine.Running()
	runs, _ := s.Store.ListRuns(ctx, 1)
	var last *store.Run
	if len(runs) > 0 {
		last = &runs[0]
	}
	next := s.Engine.NextRun(ctx)
	var nextTS int64
	if !next.IsZero() {
		nextTS = next.Unix()
	}
	b := s.Engine.Classifier().Bayes()
	writeJSON(w, 200, map[string]any{
		"paired": s.paired(), "demo": s.Demo, "running": running, "progress": progress,
		"mode": s.Engine.Setting(ctx, engine.SetMode), "next_run": nextTS, "last_run": last,
		"stats": s.Store.Stats(ctx), "model_examples": b.Examples(), "model_classes": b.ClassCounts(),
		"pair": s.Pairer.State(), "phone": s.Keeper.Status(),
	})
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dry bool `json:"dry"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if !s.paired() {
		writeErr(w, 409, gm.ErrNotPaired)
		return
	}
	if running, _ := s.Engine.Running(); running {
		writeErr(w, 409, engine.ErrBusy)
		return
	}
	go func() {
		if _, err := s.Engine.Run(context.Background(), req.Dry, "manual"); err != nil {
			s.Log.Err(err).Msg("manual run failed")
		}
	}()
	writeJSON(w, 202, map[string]bool{"started": true})
}

func qint(r *http.Request, k string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(k))
	return n
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.MessageFilter{
		Category: q.Get("category"), Label: q.Get("label"), Action: q.Get("action"), Sender: q.Get("sender"),
		Query: q.Get("q"), OnPhone: q.Get("on_phone"), Limit: qint(r, "limit"), Offset: qint(r, "offset"),
		From: int64(qint(r, "from")), To: int64(qint(r, "to")),
	}
	items, total, err := s.Store.ListMessages(r.Context(), f)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if items == nil {
		items = []store.Message{}
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.GetMessage(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	c := s.Engine.Classifier()
	writeJSON(w, 200, map[string]any{"message": m, "now": c.Classify(m.Sender, m.Body), "terms": c.Suggest(m.Body), "sender_kind": textnorm.SenderKind(m.Sender)})
}

func (s *Server) conversation(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.Store.ConversationMessages(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": msgs})
}

func (s *Server) labelMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Label string `json:"label"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	id := r.PathValue("id")
	if err := s.Store.SetLabel(ctx, id, req.Label); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.Engine.Rebuild(ctx); err != nil {
		writeErr(w, 500, err)
		return
	}
	m, _ := s.Store.GetMessage(ctx, id)
	c := s.Engine.Classifier()
	res := c.Classify(m.Sender, m.Body)
	_ = s.Store.UpdateClassification(ctx, id, res)
	m.Category, m.Confidence, m.Reasons = res.Category, res.Confidence, res.Reasons
	writeJSON(w, 200, map[string]any{"message": m, "now": res, "terms": c.Suggest(m.Body)})
}

func (s *Server) reclassify(w http.ResponseWriter, r *http.Request) {
	if err := s.Engine.Rebuild(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	n, err := s.Engine.Reclassify(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]int{"rescored": n})
}

func (s *Server) senders(w http.ResponseWriter, r *http.Request) {
	cat := r.URL.Query().Get("category")
	if cat == "" {
		cat = classify.Offer
	}
	out, err := s.Store.Senders(r.Context(), cat)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, 200, out)
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.Store.ListRules(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if rules == nil {
		rules = []classify.Rule{}
	}
	writeJSON(w, 200, rules)
}

func (s *Server) afterRuleChange(ctx context.Context) {
	if err := s.Engine.Rebuild(ctx); err != nil {
		s.Log.Err(err).Msg("rebuild")
		return
	}
	go func() {
		if _, err := s.Engine.Reclassify(context.Background()); err != nil {
			s.Log.Err(err).Msg("reclassify")
		}
	}()
}

func (s *Server) addRule(w http.ResponseWriter, r *http.Request) {
	var rule classify.Rule
	if err := readJSON(r, &rule); err != nil {
		writeErr(w, 400, err)
		return
	}
	rule.Source = "user"
	if rule.Kind == classify.KindSender {
		rule.Pattern = textnorm.Sender(rule.Pattern)
	}
	id, err := s.Store.AddRule(r.Context(), rule, false)
	if errors.Is(err, store.ErrDuplicate) {
		writeErr(w, 409, err)
		return
	} else if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.afterRuleChange(r.Context())
	rule.ID = id
	writeJSON(w, 201, rule)
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	var rule classify.Rule
	if err := readJSON(r, &rule); err != nil {
		writeErr(w, 400, err)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rule.ID = id
	if err := s.Store.UpdateRule(r.Context(), rule); err != nil {
		writeErr(w, 400, err)
		return
	}
	s.afterRuleChange(r.Context())
	writeJSON(w, 200, rule)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Store.DeleteRule(r.Context(), id); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.afterRuleChange(r.Context())
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (s *Server) test(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sender string `json:"sender"`
		Body   string `json:"body"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	c := s.Engine.Classifier()
	writeJSON(w, 200, map[string]any{"now": c.Classify(req.Sender, req.Body), "terms": c.Suggest(req.Body), "sender": textnorm.Sender(req.Sender), "sender_kind": textnorm.SenderKind(textnorm.Sender(req.Sender))})
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Store.ListRuns(r.Context(), 50)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if runs == nil {
		runs = []store.Run{}
	}
	writeJSON(w, 200, runs)
}

func (s *Server) actions(w http.ResponseWriter, r *http.Request) {
	acts, err := s.Store.ListActions(r.Context(), int64(qint(r, "run")), 500)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if acts == nil {
		acts = []store.Action{}
	}
	writeJSON(w, 200, acts)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{}
	for k := range engine.Defaults {
		out[k] = s.Engine.Setting(r.Context(), k)
	}
	writeJSON(w, 200, out)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	for k, v := range req {
		if _, ok := engine.Defaults[k]; !ok {
			writeErr(w, 400, errors.New("unknown setting "+k))
			return
		}
		v = strings.TrimSpace(v)
		if k == engine.SetMode && v != "dry" && v != "live" {
			writeErr(w, 400, errors.New("mode must be dry or live"))
			return
		}
		if k != engine.SetMode {
			if f, err := strconv.ParseFloat(v, 64); err != nil || f < 0 {
				writeErr(w, 400, errors.New(k+" must be a non-negative number"))
				return
			}
		}
		if err := s.Store.SetSetting(r.Context(), k, v); err != nil {
			writeErr(w, 500, err)
			return
		}
	}
	_ = s.Engine.Rebuild(r.Context())
	s.getSettings(w, r)
}

func (s *Server) model(w http.ResponseWriter, r *http.Request) {
	b := s.Engine.Classifier().Bayes()
	top := map[string]any{}
	for _, l := range classify.Labels {
		t := b.TopTerms(l, 25)
		if t == nil {
			t = []classify.TermLean{}
		}
		top[l] = t
	}
	writeJSON(w, 200, map[string]any{"examples": b.Examples(), "classes": b.ClassCounts(), "imported": s.Store.TrainingCount(r.Context()), "top_terms": top})
}

func (s *Server) pairState(w http.ResponseWriter, r *http.Request) {
	st := s.Pairer.State()
	out := map[string]any{"status": st.Status, "method": st.Method, "error": st.Error, "paired": s.paired(), "since": st.Since,
		"emoji": st.Emoji, "emoji_svg": st.EmojiSVG, "phone": s.Keeper.Status()}
	if st.QR != "" && st.Status == "waiting" {
		if png, err := qrcode.Encode(st.QR, qrcode.Medium, 360); err == nil {
			out["qr_png"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) pairStart(w http.ResponseWriter, r *http.Request) {
	if s.Demo {
		writeErr(w, 400, errors.New("demo mode has no phone to pair"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.Pairer.Start(ctx); err != nil {
		writeErr(w, 502, err)
		return
	}
	s.pairState(w, r)
}

func (s *Server) pairGoogle(w http.ResponseWriter, r *http.Request) {
	if s.Demo {
		writeErr(w, 400, errors.New("demo mode has no phone to pair"))
		return
	}
	var req struct {
		Cookies string `json:"cookies"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	cookies, err := gm.ParseCookies(req.Cookies)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if err := s.Pairer.StartGoogle(ctx, cookies); err != nil {
		writeErr(w, 502, err)
		return
	}
	s.pairState(w, r)
}

func (s *Server) pairCancel(w http.ResponseWriter, r *http.Request) {
	s.Pairer.Cancel()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) unpair(w http.ResponseWriter, r *http.Request) {
	if err := os.Remove(s.SessionPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeErr(w, 500, err)
		return
	}
	s.Keeper.Reset()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) importFile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeErr(w, 400, err)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	defer f.Close()
	msg, err := s.Import(r.Context(), hdr.Filename, f)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.afterRuleChange(r.Context())
	writeJSON(w, 200, map[string]string{"result": msg})
}
