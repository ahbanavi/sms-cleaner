package classify

import "testing"

func TestRulesAndBayes(t *testing.T) {
	rules := []Rule{
		{ID: 1, Kind: KindKeyword, Pattern: "لغو11", Category: Offer, Weight: 3, Enabled: true},
		{ID: 2, Kind: KindKeyword, Pattern: "رمز پویا", Category: Archive, Weight: 3, Enabled: true},
		{ID: 3, Kind: KindSender, Pattern: "+98 912 000 1111", Category: Keep, Enabled: true},
		{ID: 4, Kind: KindSenderRegex, Pattern: `^(1000|2000|3000|5000)`, Category: Offer, Weight: 1, Enabled: true},
	}
	c := New(Config{}, rules, nil)

	cases := []struct{ sender, body, want string }{
		{"30001234", "تخفیف ۴۰٪ ویژه یلدا لغو ۱۱", Offer},
		{"Mellat", "رمز پويا: ۱۲۳۴۵۶", Archive}, // Arabic ي must still match
		{"09120001111", "anything", Keep},       // sender override, normalized
		{"09350000000", "سلام خوبی", Unknown},
	}
	for _, tc := range cases {
		if got := c.Classify(tc.sender, tc.body); got.Category != tc.want {
			t.Errorf("%s %q: got %s (%v), want %s", tc.sender, tc.body, got.Category, got.Scores, tc.want)
		}
	}

	var ex []Example
	for i := 0; i < 15; i++ {
		ex = append(ex,
			Example{"09121111111", "سلام خوبی فردا میای خونه", Keep},
			Example{"09122222222", "برنده جایزه شدید روی لینک بزنید", Spam},
		)
	}
	c = New(Config{}, rules, ex)
	if got := c.Classify("09350000000", "سلام خوبی کجایی فردا میای خونه"); got.Category != Keep {
		t.Errorf("bayes keep: got %s %v", got.Category, got.Scores)
	}
	if got := c.Classify("09350000000", "شما برنده جایزه شدید روی لینک بزنید"); got.Category != Spam {
		t.Errorf("bayes spam: got %s %v", got.Category, got.Scores)
	}
}
