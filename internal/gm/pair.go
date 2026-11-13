package gm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"go.mau.fi/util/exhttp"
)

// PairState is the pairing status shown in the UI.
type PairState struct {
	Status   string `json:"status"` // idle | waiting (QR shown) | confirm (emoji shown) | success | error
	Method   string `json:"method,omitempty"`
	QR       string `json:"qr,omitempty"`
	Emoji    string `json:"emoji,omitempty"`
	EmojiSVG string `json:"emoji_svg,omitempty"`
	Error    string `json:"error,omitempty"`
	Since    int64  `json:"since"`
}

// Pairer runs one pairing at a time: by QR code, or by Google account
// (cookies + emoji confirmation on the phone).
type Pairer struct {
	SessionPath string
	Settings    exhttp.ClientSettings
	Log         zerolog.Logger
	// OnUpdate is called whenever the state changes (e.g. to print a new QR).
	OnUpdate func(PairState)
	// OnPaired is called after a new session was saved.
	OnPaired func()

	mu     sync.Mutex
	state  PairState
	cancel context.CancelFunc
}

const qrLifetime = 30 * time.Second
const qrAttempts = 8
const emojiTimeout = 5 * time.Minute

// State returns the current state.
func (p *Pairer) State() PairState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *Pairer) set(s PairState) {
	s.Since = time.Now().Unix()
	p.mu.Lock()
	if s.Method == "" {
		s.Method = p.state.Method
	}
	p.state = s
	cb := p.OnUpdate
	p.mu.Unlock()
	if cb != nil {
		cb(s)
	}
}

func (p *Pairer) begin(cancel context.CancelFunc) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.Status == "waiting" || p.state.Status == "confirm" {
		return errors.New("pairing already in progress")
	}
	p.cancel = cancel
	return nil
}

func (p *Pairer) finish(cli *libgm.Client) {
	cli.Disconnect()
	// Let the phone store the pairing before anything reconnects.
	time.Sleep(2 * time.Second)
	if err := SaveSession(p.SessionPath, cli.AuthData); err != nil {
		p.set(PairState{Status: "error", Error: "paired but could not save the session: " + err.Error()})
		return
	}
	p.set(PairState{Status: "success"})
	if p.OnPaired != nil {
		p.OnPaired()
	}
}

// Start begins QR pairing in the background. It returns once the first QR is ready.
func (p *Pairer) Start(ctx context.Context) error {
	bg, cancel := context.WithCancel(context.Background())
	if err := p.begin(cancel); err != nil {
		cancel()
		return err
	}
	cli := libgm.NewClient(libgm.NewAuthData(), nil, p.Log.With().Str("component", "libgm-pair").Logger(), p.Settings)
	cli.SetEventHandler(func(evt any) {})
	done := make(chan *gmproto.PairedData, 1)
	cb := func(data *gmproto.PairedData) { done <- data }
	cli.PairCallback.Store(&cb)
	fail := func(err error) error {
		cancel()
		p.set(PairState{Status: "error", Method: "qr", Error: err.Error()})
		return err
	}
	if err := cli.FetchConfig(ctx); err != nil {
		return fail(err)
	}
	qr, err := cli.StartLogin(bg)
	if err != nil {
		return fail(err)
	}
	p.set(PairState{Status: "waiting", Method: "qr", QR: qr})

	go func() {
		defer cancel()
		attempts := qrAttempts
		timer := time.NewTimer(qrLifetime)
		defer timer.Stop()
		for {
			select {
			case <-done:
				p.finish(cli)
				return
			case <-timer.C:
				attempts--
				if attempts <= 0 {
					cli.Disconnect()
					p.set(PairState{Status: "error", Error: "the QR code was not scanned in time"})
					return
				}
				qr, err := cli.RefreshPhoneRelay()
				if err != nil {
					cli.Disconnect()
					p.set(PairState{Status: "error", Error: "refresh QR: " + err.Error()})
					return
				}
				p.set(PairState{Status: "waiting", QR: qr})
				timer.Reset(qrLifetime)
			case <-bg.Done():
				cli.Disconnect()
				p.set(PairState{Status: "idle"})
				return
			}
		}
	}()
	return nil
}

// StartGoogle begins Google-account pairing with the given cookies. It returns
// once the phone shows the emoji prompt; confirmation is awaited in the background.
func (p *Pairer) StartGoogle(ctx context.Context, cookies map[string]string) error {
	bg, cancel := context.WithCancel(context.Background())
	if err := p.begin(cancel); err != nil {
		cancel()
		return err
	}
	fail := func(err error) error {
		cancel()
		p.set(PairState{Status: "error", Method: "google", Error: err.Error()})
		return err
	}
	ad := libgm.NewAuthData()
	ad.SetCookies(cookies)
	cli := libgm.NewClient(ad, nil, p.Log.With().Str("component", "libgm-gaia").Logger(), p.Settings)
	cli.SetEventHandler(func(evt any) {})
	if err := cli.FetchConfig(ctx); err != nil {
		return fail(fmt.Errorf("Google rejected the cookies (sign in again in a fresh window and copy them again): %w", err))
	}
	emoji, sess, err := cli.StartGaiaPairing(ctx, bg)
	if err != nil {
		cli.Disconnect()
		return fail(explainGaia(err))
	}
	p.set(PairState{Status: "confirm", Method: "google", Emoji: emoji, EmojiSVG: libgm.GetEmojiSVG(emoji)})

	go func() {
		defer cancel()
		wctx, wcancel := context.WithCancel(bg)
		defer wcancel()
		var timedOut atomic.Bool
		// libgm tells the phone to drop the prompt when the context is canceled.
		timer := time.AfterFunc(emojiTimeout, func() { timedOut.Store(true); wcancel() })
		defer timer.Stop()
		res := make(chan error, 1)
		go func() {
			_, err := cli.FinishGaiaPairing(wctx, sess)
			res <- err
		}()
		var err error
		select {
		case err = <-res:
		case <-wctx.Done():
			// Some libgm waits ignore the context; don't hang forever.
			select {
			case err = <-res:
			case <-time.After(30 * time.Second):
				err = context.Canceled
			}
		}
		if err != nil {
			cli.Disconnect()
			switch {
			case timedOut.Load():
				p.set(PairState{Status: "error", Error: "the phone did not confirm within 5 minutes"})
			case bg.Err() != nil:
				p.set(PairState{Status: "idle"}) // cancelled from the UI
			default:
				p.set(PairState{Status: "error", Error: explainGaia(err).Error()})
			}
			return
		}
		p.finish(cli)
	}()
	return nil
}

func explainGaia(err error) error {
	switch {
	case errors.Is(err, libgm.ErrNoDevicesFound):
		return errors.New("no phone found for this Google account: in Google Messages, open Device pairing and make sure pairing with your Google account is on")
	case errors.Is(err, libgm.ErrPhoneInactive):
		return errors.New("Google hasn't seen your phone recently: open Google Messages on the phone and try again")
	case errors.Is(err, libgm.ErrPairingInitTimeout):
		return errors.New("the phone didn't respond: make sure it's online and Google Messages is open")
	case errors.Is(err, libgm.ErrIncorrectEmoji):
		return errors.New("a different emoji was tapped on the phone; try again")
	case errors.Is(err, libgm.ErrPairingCancelled):
		return errors.New("pairing was cancelled on the phone")
	case errors.Is(err, libgm.ErrPairingTimeout):
		return errors.New("the phone did not confirm in time; try again")
	case errors.Is(err, context.Canceled):
		return errors.New("pairing was cancelled")
	}
	return err
}

// Cancel stops a pairing in progress.
func (p *Pairer) Cancel() {
	p.mu.Lock()
	c := p.cancel
	p.mu.Unlock()
	if c != nil {
		c()
	}
	if st := p.State().Status; st == "waiting" || st == "confirm" {
		p.set(PairState{Status: "idle"})
	}
}
