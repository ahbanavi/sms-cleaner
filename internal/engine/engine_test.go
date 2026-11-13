package engine

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/ahbanavi/sms-cleaner/internal/gm"
	"github.com/ahbanavi/sms-cleaner/internal/store"
)

func newEngine(t *testing.T, fake *gm.Fake) *Engine {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &Engine{Store: st, Log: zerolog.Nop(), Connect: func(context.Context) (gm.Backend, error) { return fake, nil }}
	if err := e.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDryRunChangesNothing(t *testing.T) {
	fake := gm.NewDemo()
	e := newEngine(t, fake)
	rep, err := e.Run(context.Background(), true, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Log) != 0 {
		t.Fatalf("dry run touched the phone: %v", fake.Log)
	}
	if rep.Stats["skipped_contact"] != 1 {
		t.Errorf("want 1 contact skipped, got %v", rep.Stats)
	}
}

func TestLiveRun(t *testing.T) {
	fake := gm.NewDemo()
	e := newEngine(t, fake)
	ctx := context.Background()
	if _, err := e.Run(ctx, false, "test"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"delete_conv c1", "archive c2", "archive c5", "delete_msg c5-m2"} {
		if !slices.Contains(fake.Log, want) {
			t.Errorf("missing op %q in %v", want, fake.Log)
		}
	}
	for _, op := range fake.Log {
		if strings.Contains(op, "c4") || strings.Contains(op, "c6") {
			t.Errorf("touched a protected conversation: %s", op)
		}
	}
	// The contact's messages are never read or stored.
	msgs, _ := e.Store.ConversationMessages(ctx, "c4")
	if len(msgs) != 0 {
		t.Errorf("contact messages stored: %v", msgs)
	}
	// Deleted offers stay searchable.
	offers, _, _ := e.Store.ListMessages(ctx, store.MessageFilter{Category: "offer", Query: "FALL50"})
	if len(offers) != 1 || offers[0].OnPhone {
		t.Errorf("offer copy not kept: %+v", offers)
	}
}
