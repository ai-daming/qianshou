package ledger

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
)

func TestConcurrentBaselineAppendsRemainGapFree(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	inputs := []NewBaseline{
		{ID: "baseline-a", AdoptionKey: "a", BriefVersionID: seed.brief.ID, IssueUpdatedAt: "a", IssueBody: "a", ResolvedDoDJSON: `[]`},
		{ID: "baseline-b", AdoptionKey: "b", BriefVersionID: seed.brief.ID, IssueUpdatedAt: "b", IssueBody: "b", ResolvedDoDJSON: `[]`},
	}
	start := make(chan struct{})
	results := make(chan DeliveryBaseline, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, input := range inputs {
		wg.Add(1)
		go func(value NewBaseline) {
			defer wg.Done()
			<-start
			baseline, err := store.AppendBaseline(context.Background(), seed.track.ID, value)
			results <- baseline
			errs <- err
		}(input)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var sequences []int
	for baseline := range results {
		sequences = append(sequences, baseline.Sequence)
	}
	sort.Ints(sequences)
	if len(sequences) != 2 || sequences[0] != 2 || sequences[1] != 3 {
		t.Fatalf("sequences = %v", sequences)
	}
}

func TestConcurrentCommandKeyRetryIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	conversation := createImplementationConversation(t, store, seed)
	input := NewAgentRun{ID: "run-1", ConversationID: conversation.ID, TrackID: seed.track.ID,
		BaselineID: seed.baseline.ID, CommandKey: "same-command", CommandJSON: `{"prompt":"same"}`}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.QueueRun(context.Background(), input)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("identical concurrent retry failed: %v", err)
		}
	}
}

func TestConcurrentSyntheticEventRetryIsIdempotent(t *testing.T) {
	store, run := seedRunningRun(t)
	input := NewRunEvent{Sequence: 1, Kind: EventStatus, PayloadJSON: `{"status":"queued"}`}
	start := make(chan struct{})
	errs := make(chan error, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- store.AppendSyntheticEvent(context.Background(), input, run.ID)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("identical concurrent synthetic event retry failed: %v", err)
		}
	}
	page, err := store.ListRunEvents(context.Background(), run.ID, 0, 10)
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("stored synthetic events = %+v, %v", page.Events, err)
	}
}

func TestConcurrentFrameSequenceConflictCannotCreateGap(t *testing.T) {
	store, run := seedRunningRun(t)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, raw := range []string{"first", "different"} {
		wg.Add(1)
		go func(payload string) {
			defer wg.Done()
			<-start
			errs <- store.AppendVendorFrame(context.Background(), NewVendorFrame{RunID: run.ID, Sequence: 1,
				RawPayload: []byte(payload), Channel: "stdout", ParseStatus: FrameIgnored, NormalizerVersion: "v1"}, nil)
		}(raw)
	}
	close(start)
	wg.Wait()
	close(errs)
	var success, conflictCount int
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflictCount++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflictCount != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflictCount)
	}
}

func TestConcurrentStopResolutionWithSameOutcomeIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	stop, err := store.OpenStopCondition(context.Background(), NewStopCondition{ID: "stop-1", TrackID: seed.track.ID,
		BaselineID: seed.baseline.ID, Kind: "DECISION", Reason: "choose", EvidenceJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.ResolveStopCondition(context.Background(), stop.ID, "ADOPTED", `{"ok":true}`)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("identical concurrent resolution failed: %v", err)
		}
	}
}
