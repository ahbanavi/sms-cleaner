package gm

import (
	"context"
	"fmt"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/encoding/prototext"
)

// ArchiveVariants are the ways the protocol might express "archive". The
// phone accepted the plain status update without archiving anything, so
// ProbeArchive tries the others on a real conversation.
var ArchiveVariants = []string{"status", "keep", "action3", "action3-status", "action4-status"}

func archiveRequest(variant, convID string) (*gmproto.UpdateConversationRequest, error) {
	status := func(s gmproto.ConversationStatus) *gmproto.UpdateConversationRequest_UpdateData {
		return &gmproto.UpdateConversationRequest_UpdateData{UpdateData: &gmproto.UpdateConversationData{
			ConversationID: convID,
			Data:           &gmproto.UpdateConversationData_Status{Status: s},
		}}
	}
	req := &gmproto.UpdateConversationRequest{ConversationID: convID}
	switch variant {
	case "status":
		req.Data = status(gmproto.ConversationStatus_ARCHIVED)
	case "keep":
		req.Data = status(gmproto.ConversationStatus_KEEP_ARCHIVED)
	case "action3":
		req.Action = gmproto.ConversationActionStatus(3)
	case "action3-status":
		req.Action = gmproto.ConversationActionStatus(3)
		req.Data = status(gmproto.ConversationStatus_ARCHIVED)
	case "action4-status":
		req.Action = gmproto.ConversationActionStatus(4)
		req.Data = status(gmproto.ConversationStatus_ARCHIVED)
	default:
		return nil, fmt.Errorf("unknown variant %q", variant)
	}
	return req, nil
}

// ProbeArchive sends one archive variant for convID and returns the raw
// response.
func (c *Client) ProbeArchive(ctx context.Context, variant, convID string) (string, error) {
	req, err := archiveRequest(variant, convID)
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
