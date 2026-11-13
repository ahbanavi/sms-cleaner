// Package gm talks to Google Messages through libgm (the protocol library of
// mautrix-gmessages), the same way messages.google.com/web does.
package gm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"go.mau.fi/util/exhttp"
)

// Folder of the Google Messages conversation list.
type Folder string

const (
	FolderInbox   Folder = "inbox"
	FolderArchive Folder = "archive"
	FolderSpam    Folder = "spam"
)

// Participant of a conversation (never "me").
type Participant struct {
	ID        string `json:"id"`
	Number    string `json:"number"`
	Name      string `json:"name"`
	ContactID string `json:"contact_id,omitempty"`
}

// Conversation summary.
type Conversation struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	IsGroup      bool          `json:"is_group"`
	Pinned       bool          `json:"pinned"`
	Unread       bool          `json:"unread"`
	Status       string        `json:"status"`
	LastTS       time.Time     `json:"last_ts"`
	LatestFromMe bool          `json:"latest_from_me"`
	Participants []Participant `json:"participants"`
	selfIDs      map[string]bool
}

// Phone returns the other side's number (for 1:1 chats), or "".
func (c Conversation) Phone() string {
	if len(c.Participants) == 1 {
		return c.Participants[0].Number
	}
	return ""
}

// Message in a conversation.
type Message struct {
	ID         string    `json:"id"`
	ConvID     string    `json:"conv_id"`
	Sender     string    `json:"sender"`
	SenderName string    `json:"sender_name"`
	Body       string    `json:"body"`
	TS         time.Time `json:"ts"`
	FromMe     bool      `json:"from_me"`
	HasMedia   bool      `json:"has_media"`
}

// Contact from the phone's address book.
type Contact struct {
	Name   string `json:"name"`
	Number string `json:"number"`
}

// Backend is what the cleaning engine needs. Implemented by *Client (real
// phone) and *Fake (demo/tests).
type Backend interface {
	Contacts(ctx context.Context) ([]Contact, error)
	Conversations(ctx context.Context, folder Folder, max int) ([]Conversation, error)
	Messages(ctx context.Context, conv Conversation, max int) ([]Message, error)
	Archive(ctx context.Context, conv Conversation) error
	BlockAndReport(ctx context.Context, conv Conversation) error
	DeleteConversation(ctx context.Context, conv Conversation) error
	DeleteMessage(ctx context.Context, msgID string) error
	Close()
}

// ---- session file ----

// ErrNotPaired means there is no usable session; pair first.
var ErrNotPaired = errors.New("not paired with a phone yet")

// LoadSession reads session.json.
func LoadSession(path string) (*libgm.AuthData, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotPaired
	} else if err != nil {
		return nil, err
	}
	ad := libgm.NewAuthData()
	if err := json.Unmarshal(b, ad); err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}
	if ad.Browser == nil || ad.TachyonAuthToken == nil || (ad.IsGoogleAccount() && !ad.HasCookies()) {
		return nil, ErrNotPaired
	}
	return ad, nil
}

// SaveSession writes session.json atomically with 0600 permissions.
func SaveSession(path string, ad *libgm.AuthData) error {
	b, err := json.Marshal(ad)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// HTTPSettings returns libgm HTTP settings, optionally through a proxy
// (http://, https:// or socks5:// URL).
func HTTPSettings(proxy string) (exhttp.ClientSettings, error) {
	s := exhttp.SensibleClientSettings
	if proxy != "" {
		return s.WithProxy(proxy)
	}
	return s, nil
}

// ---- live client ----

// Client is a connected Google Messages session.
type Client struct {
	cli         *libgm.Client
	sessionPath string
	log         zerolog.Logger

	readyOnce sync.Once
	ready     chan struct{}
	fatalOnce sync.Once
	fatal     chan error
	fatalErr  error

	activeMu   sync.Mutex
	active     bool
	activeWait chan struct{}
}

// Connect loads the session, connects, and waits until the phone confirms this
// browser session is active.
func Connect(ctx context.Context, sessionPath string, settings exhttp.ClientSettings, log zerolog.Logger) (*Client, error) {
	ad, err := LoadSession(sessionPath)
	if err != nil {
		return nil, err
	}
	c := &Client{sessionPath: sessionPath, log: log, ready: make(chan struct{}), fatal: make(chan error, 1)}
	c.cli = libgm.NewClient(ad, nil, log.With().Str("component", "libgm").Logger(), settings)
	c.cli.SetEventHandler(c.handleEvent)
	if err := c.cli.FetchConfig(ctx); err != nil {
		log.Warn().Err(err).Msg("fetch config failed, continuing")
	}
	if err := c.cli.Connect(log.WithContext(context.Background())); err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	c.save()
	select {
	case <-c.ready:
		c.activeMu.Lock()
		c.active = true
		c.activeMu.Unlock()
	case err := <-c.fatal:
		c.cli.Disconnect()
		return nil, err
	case <-time.After(45 * time.Second):
		// Some phones never send BROWSER_ACTIVE; carry on and let requests decide.
		log.Warn().Msg("no BROWSER_ACTIVE from phone after 45s, trying anyway")
	case <-ctx.Done():
		c.cli.Disconnect()
		return nil, ctx.Err()
	}
	return c, nil
}

func (c *Client) save() {
	if err := SaveSession(c.sessionPath, c.cli.AuthData); err != nil {
		c.log.Err(err).Msg("failed to save session")
	}
}

func (c *Client) setFatal(err error) {
	c.fatalOnce.Do(func() {
		c.fatalErr = err
		c.fatal <- err
	})
}

func (c *Client) handleEvent(evt any) {
	switch e := evt.(type) {
	case *events.AuthTokenRefreshed:
		c.save()
	case *gmproto.UserAlertEvent:
		c.log.Debug().Str("alert", e.GetAlertType().String()).Msg("user alert")
		switch e.GetAlertType() {
		case gmproto.AlertType_BROWSER_ACTIVE:
			c.readyOnce.Do(func() { close(c.ready) })
			c.activeMu.Lock()
			c.active = true
			if c.activeWait != nil {
				close(c.activeWait)
				c.activeWait = nil
			}
			c.activeMu.Unlock()
		case gmproto.AlertType_BROWSER_INACTIVE, gmproto.AlertType_BROWSER_INACTIVE_FROM_TIMEOUT, gmproto.AlertType_BROWSER_INACTIVE_FROM_INACTIVITY:
			c.activeMu.Lock()
			c.active = false
			c.activeMu.Unlock()
		}
	case *events.ListenFatalError:
		c.setFatal(fmt.Errorf("google messages: %w", e.Error))
	case *gmproto.RevokePairData:
		c.setFatal(errors.New("the phone unpaired this session; pair again"))
	case *events.GaiaLoggedOut:
		c.setFatal(errors.New("google account logged out; pair again"))
	case *events.PhoneNotResponding:
		c.log.Warn().Msg("phone not responding (is it online?)")
	case *events.PhoneRespondingAgain:
		c.log.Info().Msg("phone responding again")
	case *events.ListenTemporaryError:
		c.log.Warn().Err(e.Error).Msg("temporary listen error")
	}
}

// Activate makes this the active Messages-for-web session again (another
// browser tab may have taken over) and waits for the phone to confirm.
func (c *Client) Activate(ctx context.Context) error {
	c.activeMu.Lock()
	if c.active {
		c.activeMu.Unlock()
		return nil
	}
	wait := make(chan struct{})
	c.activeWait = wait
	c.activeMu.Unlock()
	if err := c.cli.SetActiveSession(ctx); err != nil {
		return fmt.Errorf("set active session: %w", err)
	}
	select {
	case <-wait:
		return nil
	case <-time.After(30 * time.Second):
		return errors.New("phone did not confirm the session within 30s (is it online?)")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Connected reports whether the long-poll connection is up and no fatal error was seen.
func (c *Client) Connected() bool { return c.fatalErr == nil && c.cli.IsConnected() }

// IsGoogleAccount reports whether the session was paired through a Google account.
func (c *Client) IsGoogleAccount() bool { return c.cli.AuthData.IsGoogleAccount() }

// Err returns a fatal error seen after connecting, if any.
func (c *Client) Err() error { return c.fatalErr }

// Close disconnects and saves the session.
func (c *Client) Close() {
	c.cli.Disconnect()
	c.save()
}

var folderMap = map[Folder]gmproto.ListConversationsRequest_Folder{
	FolderInbox:   gmproto.ListConversationsRequest_INBOX,
	FolderArchive: gmproto.ListConversationsRequest_ARCHIVE,
	FolderSpam:    gmproto.ListConversationsRequest_SPAM_BLOCKED,
}

// Conversations lists up to max conversations of a folder, newest first.
func (c *Client) Conversations(ctx context.Context, folder Folder, max int) ([]Conversation, error) {
	var out []Conversation
	var cursor *gmproto.Cursor
	seen := map[string]bool{}
	for len(out) < max {
		page := int64(min(50, max-len(out)))
		resp, err := c.cli.ListConversations(ctx, &gmproto.ListConversationsRequest{Count: page, Folder: folderMap[folder], Cursor: cursor})
		if err != nil {
			return out, err
		}
		convs := resp.GetConversations()
		added := 0
		for _, gc := range convs {
			if seen[gc.GetConversationID()] {
				continue
			}
			seen[gc.GetConversationID()] = true
			out = append(out, convertConversation(gc))
			added++
		}
		next := resp.GetCursor()
		if added == 0 || len(convs) < int(page) || next == nil || next.GetLastItemID() == "" {
			break
		}
		cursor = next
	}
	return out, nil
}

func convertConversation(gc *gmproto.Conversation) Conversation {
	conv := Conversation{
		ID:           gc.GetConversationID(),
		Name:         gc.GetName(),
		IsGroup:      gc.GetIsGroupChat(),
		Pinned:       gc.GetPinned(),
		Unread:       gc.GetUnread(),
		Status:       strings.ToLower(gc.GetStatus().String()),
		LastTS:       time.UnixMicro(gc.GetLastMessageTimestamp()),
		LatestFromMe: gc.GetLatestMessage().GetFromMe() == 1,
		selfIDs:      map[string]bool{"1": true},
	}
	for _, p := range gc.GetParticipants() {
		if p.GetIsMe() {
			conv.selfIDs[p.GetID().GetParticipantID()] = true
			continue
		}
		conv.Participants = append(conv.Participants, Participant{
			ID:        p.GetID().GetParticipantID(),
			Number:    FirstNonEmpty(p.GetID().GetNumber(), p.GetFormattedNumber()),
			Name:      FirstNonEmpty(p.GetFullName(), p.GetFirstName()),
			ContactID: p.GetContactID(),
		})
	}
	if len(conv.Participants) > 1 {
		conv.IsGroup = true
	}
	return conv
}

// Messages fetches up to max latest messages of a conversation, oldest first.
func (c *Client) Messages(ctx context.Context, conv Conversation, max int) ([]Message, error) {
	var all []*gmproto.Message
	var cursor *gmproto.Cursor
	for len(all) < max {
		page := int64(min(50, max-len(all)))
		resp, err := c.cli.FetchMessages(ctx, conv.ID, page, cursor)
		if err != nil {
			return nil, err
		}
		msgs := resp.GetMessages()
		all = append(all, msgs...)
		if len(msgs) < int(page) || len(msgs) == 0 {
			break
		}
		cursor = resp.GetCursor()
		if cursor == nil || cursor.GetLastItemID() == "" {
			oldest := msgs[0]
			cursor = &gmproto.Cursor{LastItemID: oldest.GetMessageID(), LastItemTimestamp: time.UnixMicro(oldest.GetTimestamp()).UnixMilli()}
		}
	}
	byID := map[string]Participant{}
	for _, p := range conv.Participants {
		byID[p.ID] = p
	}
	seen := map[string]bool{}
	var out []Message
	for _, m := range all {
		if seen[m.GetMessageID()] {
			continue
		}
		seen[m.GetMessageID()] = true
		fromMe, system := messageSide(m.GetMessageStatus().GetStatus(), m.GetParticipantID(), conv.selfIDs)
		if system {
			continue
		}
		var body []string
		media := false
		for _, info := range m.GetMessageInfo() {
			if t := info.GetMessageContent().GetContent(); t != "" {
				body = append(body, t)
			}
			if info.GetMediaContent() != nil {
				media = true
			}
		}
		p := byID[m.GetParticipantID()]
		if p.Number == "" && len(conv.Participants) == 1 && !fromMe {
			p = conv.Participants[0]
		}
		out = append(out, Message{
			ID: m.GetMessageID(), ConvID: conv.ID,
			Sender: FirstNonEmpty(p.Number, conv.Name), SenderName: FirstNonEmpty(p.Name, conv.Name),
			Body: strings.Join(body, "\n"), TS: time.UnixMicro(m.GetTimestamp()), FromMe: fromMe, HasMedia: media,
		})
	}
	// API returns newest first; present oldest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Contacts returns the phone's contacts with numbers.
func (c *Client) Contacts(ctx context.Context) ([]Contact, error) {
	resp, err := c.cli.ListContacts(ctx)
	if err != nil {
		return nil, err
	}
	var out []Contact
	for _, ct := range resp.GetContacts() {
		n := ct.GetNumber()
		out = append(out, Contact{Name: ct.GetName(), Number: FirstNonEmpty(n.GetNumber(), n.GetNumber2(), n.GetFormattedNumber())})
	}
	return out, nil
}

// Conversation actions missing from libgm's enum, captured from what the
// messages.google.com web client sends.
const (
	actionUnarchive = gmproto.ConversationActionStatus(3)
	actionArchive   = gmproto.ConversationActionStatus(4)
)

// Archive moves a conversation to the Archived folder. The web client sends
// action 4 with the same data as a delete; an UpdateData status of ARCHIVED
// is acknowledged by the phone but changes nothing.
func (c *Client) Archive(ctx context.Context, conv Conversation) error {
	resp, err := c.cli.UpdateConversation(ctx, archiveRequest(actionArchive, conv.ID, conv.Phone()))
	if err != nil {
		return err
	}
	if !resp.GetSuccess() {
		return errors.New("phone refused archive")
	}
	return nil
}

func archiveRequest(action gmproto.ConversationActionStatus, convID, phone string) *gmproto.UpdateConversationRequest {
	return &gmproto.UpdateConversationRequest{
		Action:         action,
		ConversationID: convID,
		Data: &gmproto.UpdateConversationRequest_DeleteData{DeleteData: &gmproto.DeleteConversationData{
			ConversationID: convID,
			Phone:          ptrNonZero(phone),
		}},
	}
}

// BlockAndReport blocks the sender and reports the conversation as spam.
func (c *Client) BlockAndReport(ctx context.Context, conv Conversation) error {
	resp, err := c.cli.UpdateConversation(ctx, &gmproto.UpdateConversationRequest{
		Action:         gmproto.ConversationActionStatus_BLOCK_AND_REPORT,
		ConversationID: conv.ID,
		Action5:        &gmproto.ConversationAction5{Field2: true},
		Data: &gmproto.UpdateConversationRequest_DeleteData{DeleteData: &gmproto.DeleteConversationData{
			ConversationID: conv.ID,
			Phone:          ptrNonZero(conv.Phone()),
		}},
	})
	if err != nil {
		return err
	}
	if !resp.GetSuccess() {
		return errors.New("phone refused block/report")
	}
	return nil
}

// DeleteConversation deletes a whole conversation from the phone.
func (c *Client) DeleteConversation(ctx context.Context, conv Conversation) error {
	return c.cli.DeleteConversation(ctx, conv.ID, conv.Phone())
}

// DeleteMessage deletes one message from the phone.
func (c *Client) DeleteMessage(ctx context.Context, msgID string) error {
	resp, err := c.cli.DeleteMessage(ctx, msgID)
	if err != nil {
		return err
	}
	if !resp.GetSuccess() {
		return errors.New("phone refused delete")
	}
	return nil
}

// FirstNonEmpty returns the first non-blank string, trimmed.
func FirstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func ptrNonZero(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// messageSide tells who wrote a message, from its status the way
// mautrix-gmessages reads it: 1–99 are outgoing, 100–199 incoming, and 200+
// are system notes (tombstones such as "SMS conversation started", deleted
// messages) that nobody wrote. Only status 0 falls back to the participant ID.
// System notes often carry the self participant ID, so checking the ID first
// would count them as replies.
func messageSide(st gmproto.MessageStatusType, participantID string, selfIDs map[string]bool) (fromMe, system bool) {
	switch {
	case st >= 200:
		return false, true
	case st >= 100:
		return false, false
	case st >= 1:
		return true, false
	}
	return selfIDs[participantID], false
}
