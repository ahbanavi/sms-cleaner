package gm

import (
	"testing"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

func TestMessageSide(t *testing.T) {
	self := map[string]bool{"1": true}
	cases := []struct {
		name         string
		st           gmproto.MessageStatusType
		pid          string
		mine, system bool
	}{
		{"incoming from self id", gmproto.MessageStatusType_INCOMING_COMPLETE, "1", false, false},
		{"incoming displayed", gmproto.MessageStatusType_INCOMING_DISPLAYED, "7", false, false},
		{"outgoing", gmproto.MessageStatusType_OUTGOING_DELIVERED, "1", true, false},
		{"outgoing failed", gmproto.MessageStatusType_OUTGOING_FAILED_GENERIC, "1", true, false},
		{"tombstone with self id", gmproto.MessageStatusType_TOMBSTONE_ONE_ON_ONE_SMS_CREATED, "1", false, true},
		{"protocol switch", gmproto.MessageStatusType_TOMBSTONE_PROTOCOL_SWITCH_TO_TEXT, "1", false, true},
		{"deleted", gmproto.MessageStatusType_MESSAGE_DELETED, "", false, true},
		{"unknown, self id", gmproto.MessageStatusType_STATUS_UNKNOWN, "1", true, false},
		{"unknown, other id", gmproto.MessageStatusType_STATUS_UNKNOWN, "7", false, false},
	}
	for _, c := range cases {
		mine, system := messageSide(c.st, c.pid, self)
		if mine != c.mine || system != c.system {
			t.Errorf("%s: got mine=%v system=%v, want %v %v", c.name, mine, system, c.mine, c.system)
		}
	}
}
