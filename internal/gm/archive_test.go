package gm

import (
	"encoding/hex"
	"testing"

	"google.golang.org/protobuf/proto"
)

// The web client's archive request for conversation "1614" from the alpha
// sender "Behpardakht", captured from messages.google.com.
const webArchive = "10041a04313631343213" + "0a0431363134" + "1a0b4265687061726461" + "6b6874"

func TestArchiveRequestMatchesWebClient(t *testing.T) {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(archiveRequest(actionArchive, "1614", "Behpardakht"))
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(b); got != webArchive {
		t.Fatalf("archive request differs from the web client's\n got %s\nwant %s", got, webArchive)
	}
}
