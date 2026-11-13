// Package notify sends run summaries to Telegram.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Telegram posts messages with a bot token to one chat.
type Telegram struct {
	Token  string
	ChatID string
	Client *http.Client
}

// Notify implements engine.Notifier.
func (t *Telegram) Notify(ctx context.Context, text string) error {
	if t == nil || t.Token == "" || t.ChatID == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]any{"chat_id": t.ChatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+t.Token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c := t.Client
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("telegram: %s: %s", resp.Status, b)
	}
	return nil
}
