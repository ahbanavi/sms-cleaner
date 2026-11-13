package gm

import (
	"context"
	"fmt"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/encoding/prototext"
)

// ArchiveVariants are archive request shapes ProbeArchive can send.
// "action4" is what the web client sends and what Archive uses; "status" and
// "keep" are acknowledged by the phone but do nothing.
var ArchiveVariants = []string{"action4", "status", "keep"}

func probeRequest(variant, convID, phone string) (*gmproto.UpdateConversationRequest, error) {
	status := func(s gmproto.ConversationStatus) *gmproto.UpdateConversationRequest {
		return &gmproto.UpdateConversationRequest{
			ConversationID: convID,
			Data: &gmproto.UpdateConversationRequest_UpdateData{UpdateData: &gmproto.UpdateConversationData{
				ConversationID: convID,
				Data:           &gmproto.UpdateConversationData_Status{Status: s},
			}},
		}
	}
	switch variant {
	case "action4":
		return archiveRequest(actionArchive, convID, phone), nil
	case "status":
		return status(gmproto.ConversationStatus_ARCHIVED), nil
	case "keep":
		return status(gmproto.ConversationStatus_KEEP_ARCHIVED), nil
	}
	return nil, fmt.Errorf("unknown variant %q", variant)
}

// ProbeArchive sends one archive variant for convID and returns the raw
// response.
func (c *Client) ProbeArchive(ctx context.Context, variant, convID string) (string, error) {
	phone := ""
	if convs, err := c.Conversations(ctx, FolderInbox, 500); err == nil {
		for _, cv := range convs {
			if cv.ID == convID {
				phone = cv.Phone()
			}
		}
	}
	req, err := probeRequest(variant, convID, phone)
	if err != nil {
		return "", err
	}
	resp, err := c.cli.UpdateConversation(ctx, req)
	if err != nil {
		return "", err
	}
	return prototext.Format(resp), nil
}

// ConversationStatus returns the status the phone reports for convID in a
// folder listing, or "" when it is not listed there.
func (c *Client) ConversationStatus(ctx context.Context, folder Folder, convID string) (string, error) {
	convs, err := c.Conversations(ctx, folder, 500)
	if err != nil {
		return "", err
	}
	for _, cv := range convs {
		if cv.ID == convID {
			return cv.Status, nil
		}
	}
	return "", nil
}
