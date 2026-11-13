package gm

import (
	"context"
	_ "embed"
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

// NewDemo returns a Fake filled with made-up sample SMS. Every brand, code
// and number is invented; 0900 numbers are not assigned to anyone.
func NewDemo() *Fake {
	f := &Fake{Msgs: map[string][]Message{}, archived: map[string]bool{}}
	now := time.Now()
	age := time.Duration(0) // how long ago the conversation's last message arrived
	add := func(id, sender, name string, contact bool, bodies ...string) {
		p := Participant{ID: id + "-p", Number: sender, Name: name}
		if contact {
			p.ContactID = "c-" + id
		}
		last := now.Add(-age)
		conv := Conversation{ID: id, Name: name, Participants: []Participant{p}, LastTS: last, selfIDs: map[string]bool{"1": true}}
		f.Convs = append(f.Convs, conv)
		for i, b := range bodies {
			ts := last.Add(-time.Duration(len(bodies)-1-i) * 26 * time.Hour)
			f.Msgs[id] = append(f.Msgs[id], Message{ID: fmt.Sprintf("%s-m%d", id, i), ConvID: id, Sender: sender, SenderName: name, Body: b, TS: ts})
		}
		age += 7 * time.Hour
	}
	add("c1", "300012345", "300012345", false,
		"جشنواره پاییزه! تا 50% تخفیف روی همه محصولات + ارسال رایگان. کد تخفیف: FALL50 لغو11",
		"فقط تا امشب: کد تخفیف ویژه SALE30 برای اولین خرید شما. لغو11")
	add("c2", "NovaBank", "NovaBank", false,
		"نوابانک\nبرداشت از حساب شما: 1,250,000 ریال\nمانده: 8,400,000\n1405/08/21",
		"رمز پویا: 482913\nاین کد محرمانه است")
	add("c3", "09000001234", "09000001234", false,
		"ابلاغیه الکترونیک قضایی برای شما صادر شد. جهت مشاهده ابلاغیه وارد شوید: sana-eblagh.example/a",
		"شما برنده خوش شانس 50 میلیون تومان جایزه شدید")
	add("c4", "+989000001122", "Ali", true, "سلام داداش فردا میای؟")
	add("c5", "KalaPlus", "KALAPLUS", false,
		"کد تایید شما در کالاپلاس: 55821",
		"سفارش شما با شماره 1234567 ارسال شد. کد رهگیری: 998877",
		"تخفیف ویژه بلک فرایدی! تا 70% تخفیف فقط تا جمعه")
	add("c6", "09000005566", "09000005566", false, "سلام، من پیک هستم دم درم، تشریف میارید پایین؟")
	add("c7", "FoodYar", "FOODYAR", false,
		"گرسنه‌ای؟ 40% تخفیف سفارش امشب با کد YAR40 تا سقف 150 هزار تومان. لغو11",
		"ارسال رایگان آخر هفته با کد FREEWEEK روی همه رستوران‌ها. لغو11")
	add("c8", "20004567", "20004567", false,
		"خرید دو عدد، سومی رایگان! فقط امروز در همه شعبه‌های فروشگاه نمونه. لغو11")
	add("c9", "PostBox", "POSTBOX", false,
		"مرسوله شما با کد رهگیری 7766554433 تحویل داده شد. پست باکس")
	add("c10", "Bargh", "BARGH", false,
		"قبض برق دوره آبان به مبلغ 450,000 ریال صادر شد. شناسه قبض: 1234567890 شناسه پرداخت: 45012")
	add("c11", "09000003344", "09000003344", false,
		"کاربر گرامی، یارانه معیشتی شما واریز شد. برای دریافت وارد شوید: yaraneh-pay.example")
	add("c12", "50009999", "50009999", false,
		"وام بدون ضامن تا 500 میلیون تومان، فوری و بدون چک. همین الان تماس بگیرید")
	add("c13", "09000007788", "09000007788", false,
		"سلام، کتابی که امانت گرفته بودی رو فردا میارم دانشگاه")
	add("c14", "CineClub", "CINECLUB", false,
		"بلیت دوم رایگان! کد CINE2FOR1 را هنگام خرید در اپ وارد کنید. لغو11")
	add("c15", "NovaInsure", "NOVAINSURE", false,
		"بیمه‌نامه شخص ثالث شما تا 10 روز دیگر به پایان می‌رسد. برای تمدید به اپ نوا بیمه سر بزنید")
	add("c16", "1000777", "1000777", false,
		"مشترک گرامی، تنها 3 روز از اعتبار بسته شما باقی مانده است")
	add("c17", "09000008899", "09000008899", false,
		"این شماره جدید منه، لطفا ذخیره کن")
	f.Contact = []Contact{{Name: "Ali", Number: "+989000001122"}}
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

// DemoTraining is made-up labeled SMS (.jsonl) that demo mode learns from, so
// the sample inbox is sorted the way a trained install sorts a real one.
//
//go:embed demo_training.jsonl
var DemoTraining []byte
