// Package textnorm normalizes Persian/English SMS text and sender IDs so rules,
// the Bayes model and search all see the same form.
package textnorm

import (
	"regexp"
	"strings"
	"unicode"
)

var replacer = strings.NewReplacer(
	// Arabic letter forms -> Persian
	"ي", "ی", // ي -> ی
	"ى", "ی", // ى -> ی
	"ك", "ک", // ك -> ک
	"ة", "ه", // ة -> ه
	"ۀ", "ه", // ۀ -> ه
	"أ", "ا", // أ -> ا
	"إ", "ا", // إ -> ا
	"آ", "ا", // آ -> ا
	"ؤ", "و", // ؤ -> و
	// zero-width non-joiner splits words in Persian; treat as a space
	"\u200C", " ",
	// other invisible marks
	"\u200B", "", "\u200D", "", "\u200E", "", "\u200F", "", "\uFEFF", "", "\u0640", "",
	// Persian / Arabic punctuation
	"،", ",", "٬", ",", "؛", ";", "؟", "?", "٪", "%",
)

// Text returns the canonical form used for matching: Persian letter forms
// unified, Persian/Arabic digits mapped to ASCII, diacritics removed,
// lower-cased, whitespace collapsed.
func Text(s string) string {
	s = replacer.Replace(s)
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case r >= '۰' && r <= '۹':
			r = '0' + (r - '۰')
		case r >= '٠' && r <= '٩':
			r = '0' + (r - '٠')
		case (r >= '\u064B' && r <= '\u065F') || r == '\u0670':
			continue // harakat
		}
		if unicode.IsSpace(r) {
			if !space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.TrimRight(b.String(), " ")
}

// NoSpace is Text with all spaces removed, so "لغو 11" and "لغو11" match.
func NoSpace(s string) string { return strings.ReplaceAll(s, " ", "") }

var digitRun = regexp.MustCompile(`[0-9#][0-9#,./:\-]*[0-9#]|[0-9#]`)

// Tokens splits normalized text into word tokens. Digit runs collapse to "#",
// so amounts, OTPs and dates don't fragment the vocabulary.
func Tokens(norm string) []string {
	norm = digitRun.ReplaceAllString(norm, " # ")
	f := strings.FieldsFunc(norm, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '#' || r == '%')
	})
	out := f[:0]
	for _, t := range f {
		if t == "#" || len([]rune(t)) >= 2 || t == "%" {
			out = append(out, t)
		}
	}
	return out
}

// Sender kinds.
const (
	KindMobile = "mobile" // Iranian personal mobile 09xx
	KindAdLine = "adline" // bulk/advertising lines: 1000.., 2000.., 3000.., 5000.., 9810..
	KindShort  = "short"  // short codes (<= 8 digits)
	KindIntl   = "intl"   // non-Iranian international number
	KindAlpha  = "alpha"  // alphanumeric sender ID
	KindPhone  = "phone"  // other numbers (landlines etc.)
)

var (
	reMobile = regexp.MustCompile(`^(?:\+98|0098|98|0)?(9\d{9})$`)
	reAdLine = regexp.MustCompile(`^(?:\+98|0098)?(?:1000|2000|3000|5000|9810|9820|9830)\d*$`)
)

// Sender returns a canonical sender: mobiles become 09xxxxxxxxx, numbers lose
// separators, alphanumeric IDs are lower-cased.
func Sender(s string) string {
	s = Text(s)
	if s == "" {
		return ""
	}
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '(' || r == ')' || r == '.' {
			return -1
		}
		return r
	}, s)
	if m := reMobile.FindStringSubmatch(compact); m != nil {
		return "0" + m[1]
	}
	if isNumber(compact) {
		return compact
	}
	return s
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '+' && i == 0 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// SenderKind classifies a canonical sender (output of Sender).
func SenderKind(sender string) string {
	switch {
	case sender == "":
		return KindAlpha
	case strings.HasPrefix(sender, "09") && len(sender) == 11 && isNumber(sender):
		return KindMobile
	case reAdLine.MatchString(sender):
		return KindAdLine
	case isNumber(sender) && !strings.HasPrefix(sender, "+") && len(sender) <= 8:
		return KindShort
	case isNumber(sender) && strings.HasPrefix(sender, "+") && !strings.HasPrefix(sender, "+98"):
		return KindIntl
	case isNumber(sender):
		return KindPhone
	default:
		return KindAlpha
	}
}

// PhoneKey returns a comparison key for matching a sender against the contact
// list: the last 10 digits of a long number, or the whole digit string.
func PhoneKey(s string) string {
	var d strings.Builder
	for _, r := range Text(s) {
		if r >= '0' && r <= '9' {
			d.WriteRune(r)
		}
	}
	k := d.String()
	if len(k) > 10 {
		k = k[len(k)-10:]
	}
	return k
}

// MaskMobile hides the last 7 digits of a personal mobile number for logs/notifications.
func MaskMobile(sender string) string {
	if SenderKind(sender) == KindMobile {
		return sender[:4] + "*******"
	}
	return sender
}
