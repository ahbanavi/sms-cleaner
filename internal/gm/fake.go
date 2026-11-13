package gm

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Fake is an in-memory Backend for demo mode and tests. It never touches a phone.
type Fake struct {
	mu       sync.Mutex
	Convs    []Conversation
	Msgs     map[string][]Message
	Contact  []Contact
	Log      []string // operations performed, for tests
	archived map[string]bool
}

// NewDemo returns a Fake filled with made-up sample SMS.
func NewDemo() *Fake {
	f := &Fake{Msgs: map[string][]Message{}, archived: map[string]bool{}}
	now := time.Now()
	add := func(id, sender, name string, contact bool, bodies ...string) {
		p := Participant{ID: id + "-p", Number: sender, Name: name}
		if contact {
			p.ContactID = "c-" + id
		}
		conv := Conversation{ID: id, Name: name, Participants: []Participant{p}, LastTS: now, selfIDs: map[string]bool{"1": true}}
		f.Convs = append(f.Convs, conv)
		for i, b := range bodies {
			f.Msgs[id] = append(f.Msgs[id], Message{ID: fmt.Sprintf("%s-m%d", id, i), ConvID: id, Sender: sender, SenderName: name, Body: b, TS: now.Add(time.Duration(i-len(bodies)) * time.Hour)})
		}
	}
	add("c1", "300012345", "300012345", false,
		"جشنواره پاییزه! تا 50% تخفیف روی همه محصولات + ارسال رایگان. کد تخفیف: FALL50 لغو11",
		"فقط تا امشب: کد تخفیف ویژه SALE30 برای اولین خرید شما. لغو11")
	add("c2", "Mellat", "Mellat", false,
		"بانک ملت\nبرداشت از حساب شما: 1,250,000 ریال\nمانده: 8,400,000\n1405/08/21",
		"رمز پویا: 482913\nاین کد محرمانه است")
	add("c3", "09351234567", "09351234567", false,
		"ابلاغیه الکترونیک قضایی برای شما صادر شد. جهت مشاهده ابلاغیه وارد شوید: adliran-sana.xyz/a",
		"شما برنده خوش شانس 50 میلیون تومان جایزه شدید")
	add("c4", "+989121112233", "Ali", true, "سلام داداش فردا میای؟")
	add("c5", "Digikala", "DIGIKALA", false,
		"کد تایید شما در دیجی\u200Cکالا: 55821",
		"سفارش شما با شماره 1234567 ارسال شد. کد رهگیری: 998877",
		"تخفیف ویژه بلک فرایدی! تا 70% تخفیف فقط تا جمعه")
	add("c6", "09125556677", "09125556677", false, "سلام، من پیک هستم دم درم، تشریف میارید پایین؟")
	f.Contact = []Contact{{Name: "Ali", Number: "+989121112233"}}
	return f
}

func (f *Fake) record(op string) {
	f.mu.Lock()
	f.Log = append(f.Log, op)
	f.mu.Unlock()
}

// Contacts implements Backend.
func (f *Fake) Contacts(ctx context.Context) ([]Contact, error) { return f.Contact, nil }

// Conversations implements Backend.
func (f *Fake) Conversations(ctx context.Context, folder Folder, max int) ([]Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Conversation
	for _, c := range f.Convs {
		if (folder == FolderArchive) == f.archived[c.ID] {
			out = append(out, c)
		}
	}
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

// Messages implements Backend.
func (f *Fake) Messages(ctx context.Context, conv Conversation, max int) ([]Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.Msgs[conv.ID]
	if len(m) > max {
		m = m[len(m)-max:]
	}
	return append([]Message(nil), m...), nil
}

// Archive implements Backend.
func (f *Fake) Archive(ctx context.Context, conv Conversation) error {
	f.mu.Lock()
	f.archived[conv.ID] = true
	f.mu.Unlock()
	f.record("archive " + conv.ID)
	return nil
}

// BlockAndReport implements Backend.
func (f *Fake) BlockAndReport(ctx context.Context, conv Conversation) error {
	f.record("block " + conv.ID)
	return nil
}

// DeleteConversation implements Backend.
func (f *Fake) DeleteConversation(ctx context.Context, conv Conversation) error {
	f.mu.Lock()
	for i, c := range f.Convs {
		if c.ID == conv.ID {
			f.Convs = append(f.Convs[:i], f.Convs[i+1:]...)
			break
		}
	}
	delete(f.Msgs, conv.ID)
	f.mu.Unlock()
	f.record("delete_conv " + conv.ID)
	return nil
}

// DeleteMessage implements Backend.
func (f *Fake) DeleteMessage(ctx context.Context, msgID string) error {
	f.mu.Lock()
	for cid, ms := range f.Msgs {
		for i, m := range ms {
			if m.ID == msgID {
				f.Msgs[cid] = append(ms[:i], ms[i+1:]...)
				break
			}
		}
	}
	f.mu.Unlock()
	f.record("delete_msg " + msgID)
	return nil
}

// Close implements Backend.
func (f *Fake) Close() {}
