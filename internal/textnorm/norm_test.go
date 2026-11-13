package textnorm

import "testing"

func TestText(t *testing.T) {
	for in, want := range map[string]string{
		"لغو ۱۱":    "لغو 11",
		"لغو١١":     "لغو11",
		"رمز پويا":  "رمز پویا",
		"كد  تخفيف": "کد تخفیف",
	} {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q %U, want %q", in, got, []rune(got), want)
		}
	}
	if got := Sender("+98 900 000 1111"); got != "09000001111" {
		t.Errorf("Sender = %q", got)
	}
	if k := SenderKind("30001234"); k != KindAdLine {
		t.Errorf("kind = %s", k)
	}
}
