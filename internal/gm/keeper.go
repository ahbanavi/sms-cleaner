package gm

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/exhttp"
)

// Keeper holds one long-lived connection for the web server. Staying
// connected keeps Google-account cookies and tokens refreshed (they expire
// when a session sits offline for hours) and makes runs start instantly.
type Keeper struct {
	SessionPath string
	Settings    exhttp.ClientSettings
	Log         zerolog.Logger

	mu      sync.Mutex
	cli     *Client
	lastErr error
	since   time.Time
	wake    chan struct{}
}

// KeeperStatus is shown in the UI.
type KeeperStatus struct {
	Connected     bool   `json:"connected"`
	GoogleAccount bool   `json:"google_account"`
	Error         string `json:"error,omitempty"`
	Since         int64  `json:"since,omitempty"`
}

// Status reports the connection state.
func (k *Keeper) Status() KeeperStatus {
	k.mu.Lock()
	defer k.mu.Unlock()
	st := KeeperStatus{}
	if k.cli != nil {
		st.Connected = k.cli.Connected()
		st.GoogleAccount = k.cli.IsGoogleAccount()
		if err := k.cli.Err(); err != nil {
			st.Error = err.Error()
		}
	}
	if st.Error == "" && k.lastErr != nil && !errors.Is(k.lastErr, ErrNotPaired) {
		st.Error = k.lastErr.Error()
	}
	if !k.since.IsZero() {
		st.Since = k.since.Unix()
	}
	return st
}

// shared wraps the kept client so the engine's Close leaves it connected.
type shared struct{ *Client }

func (shared) Close() {}

// Backend returns the connected client (connecting first if needed) and makes
// it the active Messages-for-web session.
func (k *Keeper) Backend(ctx context.Context) (Backend, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cli != nil && k.cli.Connected() {
		if err := k.cli.Activate(ctx); err == nil {
			return shared{k.cli}, nil
		} else {
			k.Log.Warn().Err(err).Msg("re-activating kept session failed, reconnecting")
		}
	}
	if err := k.connectLocked(ctx); err != nil {
		return nil, err
	}
	return shared{k.cli}, nil
}

func (k *Keeper) connectLocked(ctx context.Context) error {
	if k.cli != nil {
		k.cli.Close()
		k.cli = nil
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cli, err := Connect(cctx, k.SessionPath, k.Settings, k.Log)
	k.lastErr = err
	if err != nil {
		return err
	}
	k.cli = cli
	k.since = time.Now()
	k.Log.Info().Bool("google_account", cli.IsGoogleAccount()).Msg("connected to phone")
	return nil
}

// Reset drops the current connection (after pairing or unpairing) and
// reconnects soon.
func (k *Keeper) Reset() {
	k.mu.Lock()
	if k.cli != nil {
		k.cli.Close()
		k.cli = nil
	}
	k.lastErr = nil
	k.mu.Unlock()
	select {
	case k.wake <- struct{}{}:
	default:
	}
}

// Run keeps the connection up until ctx ends, retrying with backoff.
func (k *Keeper) Run(ctx context.Context) {
	k.mu.Lock()
	if k.wake == nil {
		k.wake = make(chan struct{}, 1)
	}
	k.mu.Unlock()
	backoff := time.Minute
	for {
		k.mu.Lock()
		healthy := k.cli != nil && k.cli.Connected()
		var err error
		if !healthy {
			if _, perr := LoadSession(k.SessionPath); perr == nil {
				err = k.connectLocked(ctx)
			} else if k.cli != nil {
				k.cli.Close()
				k.cli = nil
			}
		}
		k.mu.Unlock()
		wait := 2 * time.Minute
		if err != nil {
			k.Log.Warn().Err(err).Dur("retry_in", backoff).Msg("phone connection failed")
			wait = backoff
			backoff = min(backoff*2, 30*time.Minute)
		} else if !healthy {
			backoff = time.Minute
		}
		select {
		case <-ctx.Done():
			k.mu.Lock()
			if k.cli != nil {
				k.cli.Close()
			}
			k.mu.Unlock()
			return
		case <-k.wake:
			backoff = time.Minute
		case <-time.After(wait):
		}
	}
}
