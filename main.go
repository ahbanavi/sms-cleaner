// Command sms-cleaner keeps a Google Messages inbox clean: it blocks and
// reports spam, removes promotional SMS (keeping a searchable copy), archives
// service notices, and never touches conversations with your contacts.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	qrcode "github.com/skip2/go-qrcode"
	"go.mau.fi/util/exhttp"

	"github.com/ahbanavi/sms-cleaner/internal/classify"
	"github.com/ahbanavi/sms-cleaner/internal/engine"
	"github.com/ahbanavi/sms-cleaner/internal/gm"
	"github.com/ahbanavi/sms-cleaner/internal/notify"
	"github.com/ahbanavi/sms-cleaner/internal/store"
	"github.com/ahbanavi/sms-cleaner/internal/web"
)

var version = "dev"

type config struct {
	dataDir, listen, proxy, user, pass, tgToken, tgChat, logLevel string
	demo                                                          bool
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadConfig() config {
	return config{
		dataDir:  env("SMSC_DATA_DIR", "./data"),
		listen:   env("SMSC_LISTEN", ":8080"),
		proxy:    os.Getenv("SMSC_PROXY"),
		user:     env("SMSC_UI_USER", "admin"),
		pass:     os.Getenv("SMSC_UI_PASSWORD"),
		tgToken:  os.Getenv("SMSC_TELEGRAM_TOKEN"),
		tgChat:   os.Getenv("SMSC_TELEGRAM_CHAT_ID"),
		logLevel: env("SMSC_LOG_LEVEL", "info"),
		demo:     os.Getenv("SMSC_DEMO") == "1",
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `sms-cleaner %s

Usage: sms-cleaner <command> [flags]

  serve            web UI + scheduler (default)
  pair [-google]   pair with your phone: QR code, or -google (cookies on stdin + emoji)
  ping             connect to the phone once and disconnect (keeps the session alive)
  run [-live]      one cleaning pass (dry unless -live)
  import FILE...   import training (.jsonl: {"sender","body","label"}) or rules (.json array)
  eval             cross-validate the classifier on labeled data
  export-rules     print all rules as JSON

Environment: SMSC_DATA_DIR (./data), SMSC_LISTEN (:8080), SMSC_PROXY,
SMSC_UI_USER/SMSC_UI_PASSWORD, SMSC_TELEGRAM_TOKEN/SMSC_TELEGRAM_CHAT_ID,
SMSC_DEMO=1, SMSC_LOG_LEVEL.
`, version)
}

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	cfg := loadConfig()
	lvl, err := zerolog.ParseLevel(cfg.logLevel)
	if err != nil {
		lvl = zerolog.InfoLevel
	}
	setupTimeZone()
	// Colors only on a terminal; log files (Android) and docker logs stay plain.
	fi, _ := os.Stderr.Stat()
	plain := fi == nil || fi.Mode()&os.ModeCharDevice == 0
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.DateTime, NoColor: plain}).Level(lvl).With().Timestamp().Logger()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "serve", "pair", "ping", "probe-archive", "run", "import", "eval", "export-rules":
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err := os.MkdirAll(cfg.dataDir, 0o700); err != nil {
		log.Fatal().Err(err).Msg("data dir")
	}
	setupDNS(log)
	sessionPath := filepath.Join(cfg.dataDir, "session.json")
	settings, err := gm.HTTPSettings(cfg.proxy)
	if err != nil {
		log.Fatal().Err(err).Msg("proxy")
	}

	if cmd == "ping" {
		os.Exit(cmdPing(ctx, sessionPath, settings, log))
	}
	if cmd == "probe-archive" {
		os.Exit(cmdProbeArchive(ctx, sessionPath, settings, log, args))
	}

	if cmd == "pair" {
		fs := flag.NewFlagSet("pair", flag.ExitOnError)
		google := fs.Bool("google", false, "pair through your Google account: paste cookies (a 'Copy as cURL' of the messages.google.com page request) on stdin")
		_ = fs.Parse(args)
		os.Exit(cmdPair(ctx, sessionPath, settings, log, *google))
	}

	st, err := store.Open(filepath.Join(cfg.dataDir, "sms-cleaner.db"))
	if err != nil {
		log.Fatal().Err(err).Msg("open db")
	}
	defer st.Close()
	eng := &engine.Engine{Store: st, Log: log}
	if cfg.tgToken != "" && cfg.tgChat != "" {
		eng.Notifier = &notify.Telegram{Token: cfg.tgToken, ChatID: cfg.tgChat}
	}
	var demo *gm.Fake
	if cfg.demo {
		demo = gm.NewDemo()
	}
	eng.Connect = func(ctx context.Context) (gm.Backend, error) {
		if demo != nil {
			return demo, nil
		}
		cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		return gm.Connect(cctx, sessionPath, settings, log)
	}
	if err := eng.Init(ctx); err != nil {
		log.Fatal().Err(err).Msg("init")
	}
	importDir(ctx, st, filepath.Join(cfg.dataDir, "import"), log)
	if cfg.demo && len(st.TrainingCount(ctx)) == 0 {
		if msg, err := importReader(ctx, st, "demo.jsonl", bytes.NewReader(gm.DemoTraining)); err != nil {
			log.Warn().Err(err).Msg("demo training")
		} else {
			log.Info().Str("result", msg).Msg("loaded demo training")
		}
	}
	if err := eng.Rebuild(ctx); err != nil {
		log.Fatal().Err(err).Msg("rebuild")
	}

	switch cmd {
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		live := fs.Bool("live", false, "apply actions on the phone")
		_ = fs.Parse(args)
		rep, err := eng.Run(ctx, !*live, "cli")
		printJSON(rep)
		if err != nil {
			log.Fatal().Err(err).Msg("run failed")
		}
	case "import":
		for _, f := range args {
			msg, err := importPath(ctx, st, f)
			if err != nil {
				log.Fatal().Err(err).Str("file", f).Msg("import")
			}
			fmt.Println(f+":", msg)
		}
		if _, err := eng.Reclassify(ctx); err != nil {
			log.Fatal().Err(err).Msg("reclassify")
		}
	case "eval":
		fs := flag.NewFlagSet("eval", flag.ExitOnError)
		verbose := fs.Bool("v", false, "print messages that would be wrongly deleted")
		_ = fs.Parse(args)
		cmdEval(ctx, st, *verbose)
	case "export-rules":
		rules, _ := st.ListRules(ctx)
		printJSON(rules)
	case "serve":
		keeper := &gm.Keeper{SessionPath: sessionPath, Settings: settings, Log: log}
		if !cfg.demo {
			eng.Connect = keeper.Backend
			eng.Paired = func() bool { _, err := gm.LoadSession(sessionPath); return err == nil }
			go keeper.Run(ctx)
		}
		pairer := &gm.Pairer{SessionPath: sessionPath, Settings: settings, Log: log, OnPaired: keeper.Reset}
		srv := &web.Server{Engine: eng, Store: st, Pairer: pairer, Keeper: keeper, SessionPath: sessionPath, Demo: cfg.demo, User: cfg.user, Pass: cfg.pass, Log: log,
			Import: func(ctx context.Context, name string, r io.Reader) (string, error) {
				return importReader(ctx, st, name, r)
			}}
		go eng.Scheduler(ctx)
		hs := &http.Server{Addr: cfg.listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = hs.Shutdown(sctx)
		}()
		log.Info().Str("listen", cfg.listen).Bool("demo", cfg.demo).Bool("auth", cfg.pass != "").Msg("sms-cleaner serving")
		if cfg.pass == "" {
			log.Warn().Msg("SMSC_UI_PASSWORD is empty: the UI (your SMS) is open to anyone who can reach this port")
		}
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("http")
		}
	}
}

// cmdPing connects once so Google sees the session in use (Messages for web
// signs out sessions idle for about two weeks) and refreshed tokens and
// cookies get saved, then disconnects.
func cmdPing(ctx context.Context, sessionPath string, settings exhttp.ClientSettings, log zerolog.Logger) int {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cli, err := gm.Connect(cctx, sessionPath, settings, log)
	if err != nil {
		log.Error().Err(err).Msg("could not reach the phone")
		return 1
	}
	// Give token-refresh events a moment to arrive and be saved.
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
	}
	cli.Close()
	log.Info().Msg("phone session is alive")
	return 0
}

// cmdProbeArchive tries archive request variants on one conversation until
// the phone really moves it out of the inbox. Diagnostic: it changes the phone.
func cmdProbeArchive(ctx context.Context, sessionPath string, settings exhttp.ClientSettings, log zerolog.Logger, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: sms-cleaner probe-archive CONVERSATION_ID [VARIANT...]")
		fmt.Fprintln(os.Stderr, "variants:", strings.Join(gm.ArchiveVariants, ", "))
		return 2
	}
	convID, variants := args[0], args[1:]
	if len(variants) == 0 {
		variants = gm.ArchiveVariants
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	cli, err := gm.Connect(cctx, sessionPath, settings, log)
	cancel()
	if err != nil {
		log.Error().Err(err).Msg("could not reach the phone")
		return 1
	}
	defer cli.Close()
	where := func() (string, bool) {
		in, err1 := cli.ConversationStatus(ctx, gm.FolderInbox, convID)
		ar, err2 := cli.ConversationStatus(ctx, gm.FolderArchive, convID)
		ok := err1 == nil && err2 == nil
		return fmt.Sprintf("inbox=%q archive=%q errs=%v,%v", in, ar, err1, err2), ok && in == ""
	}
	before, _ := where()
	fmt.Println("before:", before)
	for _, v := range variants {
		resp, err := cli.ProbeArchive(ctx, v, convID)
		fmt.Printf("variant %s: err=%v resp=%q\n", v, err, strings.TrimSpace(resp))
		time.Sleep(10 * time.Second)
		after, gone := where()
		fmt.Println("  after:", after)
		if err == nil && gone {
			fmt.Println("WORKS:", v)
			return 0
		}
	}
	return 3
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func cmdPair(ctx context.Context, sessionPath string, settings exhttp.ClientSettings, log zerolog.Logger, google bool) int {
	p := &gm.Pairer{SessionPath: sessionPath, Settings: settings, Log: log}
	done := make(chan gm.PairState, 1)
	p.OnUpdate = func(st gm.PairState) {
		switch st.Status {
		case "waiting":
			q, err := qrcode.New(st.QR, qrcode.Low)
			if err == nil {
				fmt.Println(q.ToSmallString(false))
			}
			fmt.Println("Google Messages › profile › Device pairing › QR code scanner. Scan within 30s (a new code follows).")
		case "confirm":
			fmt.Printf("\nOn your phone, Google Messages asks to confirm. Tap this emoji:  %s\n\n", st.Emoji)
		case "success", "error", "idle":
			done <- st
		}
	}
	var err error
	if google {
		fmt.Fprintln(os.Stderr, "Paste the cookies (cURL command, Cookie header or JSON), then press Ctrl-D:")
		raw, rerr := io.ReadAll(os.Stdin)
		if rerr != nil {
			log.Error().Err(rerr).Msg("read stdin")
			return 1
		}
		cookies, perr := gm.ParseCookies(string(raw))
		if perr != nil {
			fmt.Println("Cookies:", perr)
			return 1
		}
		err = p.StartGoogle(ctx, cookies)
	} else {
		err = p.Start(ctx)
	}
	if err != nil {
		log.Error().Err(err).Msg("start pairing")
		return 1
	}
	select {
	case st := <-done:
		if st.Status == "success" {
			fmt.Println("Paired. Session saved to", sessionPath)
			return 0
		}
		fmt.Println("Pairing failed:", st.Error)
		return 1
	case <-ctx.Done():
		p.Cancel()
		return 1
	}
}

// ---- import ----

func importDir(ctx context.Context, st *store.Store, dir string, log zerolog.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".json")) {
			continue
		}
		path := filepath.Join(dir, name)
		msg, err := importPath(ctx, st, path)
		if err != nil {
			log.Warn().Err(err).Str("file", path).Msg("auto-import failed")
			continue
		}
		log.Info().Str("file", path).Str("result", msg).Msg("auto-imported")
		_ = os.Rename(path, path+".done")
	}
}

func importPath(ctx context.Context, st *store.Store, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return importReader(ctx, st, filepath.Base(path), f)
}

func importReader(ctx context.Context, st *store.Store, name string, r io.Reader) (string, error) {
	switch {
	case strings.HasSuffix(name, ".jsonl"):
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		n, bad := 0, 0
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var ex struct{ Sender, Body, Label string }
			if json.Unmarshal([]byte(line), &ex) != nil || st.AddTraining(ctx, ex.Sender, ex.Body, ex.Label, "import") != nil {
				bad++
				continue
			}
			n++
		}
		return fmt.Sprintf("%d training examples imported, %d skipped", n, bad), sc.Err()
	case strings.HasSuffix(name, ".json"):
		var rules []classify.Rule
		if err := json.NewDecoder(r).Decode(&rules); err != nil {
			return "", fmt.Errorf("rules file must be a JSON array of rules: %w", err)
		}
		n, bad := 0, 0
		for _, rule := range rules {
			if rule.Source == "" {
				rule.Source = "import"
			}
			if _, err := st.AddRule(ctx, rule, true); err != nil {
				bad++
				continue
			}
			n++
		}
		return fmt.Sprintf("%d rules imported, %d rejected", n, bad), nil
	}
	return "", errors.New("unsupported file: use .jsonl (training) or .json (rules)")
}

// ---- eval ----

func cmdEval(ctx context.Context, st *store.Store, verbose bool) {
	ex, err := st.Examples(ctx)
	if err != nil || len(ex) < 10 {
		fmt.Println("need at least 10 labeled examples (import training or label messages in the UI)")
		return
	}
	rules, _ := st.ListRules(ctx)
	rng := rand.New(rand.NewPCG(1, 2))
	rng.Shuffle(len(ex), func(i, j int) { ex[i], ex[j] = ex[j], ex[i] })
	const k = 5
	conf := map[string]map[string]int{}
	cfg := classify.Config{}
	for fold := 0; fold < k; fold++ {
		var train, test []classify.Example
		for i, e := range ex {
			if i%k == fold {
				test = append(test, e)
			} else {
				train = append(train, e)
			}
		}
		c := classify.New(cfg, rules, train)
		for _, e := range test {
			res := c.Classify(e.Sender, e.Body)
			got := res.Category
			if verbose && (got == classify.Spam || got == classify.Offer) && (e.Label == classify.Archive || e.Label == classify.Keep) {
				b := []rune(strings.ReplaceAll(e.Body, "\n", " "))
				if len(b) > 140 {
					b = b[:140]
				}
				fmt.Printf("[%s→%s] %s: %s\n    because: %s %v\n", e.Label, got, e.Sender, string(b), res.Summary(), res.Scores)
			}
			if conf[e.Label] == nil {
				conf[e.Label] = map[string]int{}
			}
			conf[e.Label][got]++
		}
	}
	cols := append(append([]string{}, classify.Labels...), classify.Unknown)
	fmt.Printf("5-fold cross-validation on %d labeled SMS (rules + learned model)\n\n%-9s", len(ex), "truth\\got")
	for _, c := range cols {
		fmt.Printf("%9s", c)
	}
	fmt.Println()
	var total, right, decided, harmful int
	labels := make([]string, 0, len(conf))
	for l := range conf {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, l := range labels {
		fmt.Printf("%-9s", l)
		for _, c := range cols {
			n := conf[l][c]
			fmt.Printf("%9d", n)
			total += n
			if c != classify.Unknown {
				decided += n
			}
			if c == l {
				right += n
			}
			// Destructive mistakes: something worth keeping would be deleted or blocked.
			if (c == classify.Spam || c == classify.Offer) && (l == classify.Archive || l == classify.Keep) {
				harmful += n
			}
		}
		fmt.Println()
	}
	fmt.Printf("\ndecided %.0f%% of messages; %.1f%% of decided are correct; %d would wrongly delete something worth keeping\n",
		100*float64(decided)/float64(total), 100*float64(right)/float64(max(decided, 1)), harmful)
}
