package ledger

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRawFrameInspectionIsExactAndRequiresServerToBeStopped(t *testing.T) {
	home := t.TempDir() + "/home"
	store, err := Open(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	seed := seedTrack(t, store, "track-inspect")
	conversation := createImplementationConversation(t, store, seed)
	run, err := store.QueueRun(context.Background(), NewAgentRun{ID: "run-inspect", ConversationID: conversation.ID,
		TrackID: seed.track.ID, BaselineID: seed.baseline.ID, CommandKey: "inspect", CommandJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("secret\x00unredacted\n")
	if err := store.AppendVendorFrame(context.Background(), NewVendorFrame{RunID: run.ID, Sequence: 1,
		RawPayload: raw, Channel: "stdout", ParseStatus: FrameIgnored, NormalizerVersion: "v1"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRawFrame(context.Background(), home, run.ID, 1); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("live inspection error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRawFrame(context.Background(), home, run.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("raw = %q, want exact %q", got, raw)
	}
}
