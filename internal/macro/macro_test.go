package macro

import (
	"context"
	"testing"
	"time"
)

func TestStoreSaveLoadMergeAndReplay(t *testing.T) {
	store := NewStore(t.TempDir())
	s1 := Script{
		Name:       "日常领取",
		DurationMS: 40,
		Events: []Event{
			{OffsetMS: 0, Kind: EventTouchDown, NX: 0.5, NY: 0.5},
			{OffsetMS: 20, Kind: EventTouchUp},
		},
	}
	s2 := Script{
		Name:       "自动副本",
		DurationMS: 30,
		Events: []Event{
			{OffsetMS: 10, Kind: EventKeyDown, Usage: 0x04},
			{OffsetMS: 25, Kind: EventKeyUp, Usage: 0x04},
		},
	}
	if err := store.Save(s1); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(s2); err != nil {
		t.Fatal(err)
	}
	list, err := store.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("List: %d %v", len(list), err)
	}
	merged, err := Merge("合集", []Script{s1, s2}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Events) != 4 || merged.Events[2].OffsetMS != 60 {
		t.Fatalf("unexpected merged script: %+v", merged)
	}
	var played []Event
	err = Replay(context.Background(), s1, ReplayOptions{Loops: 2, Speed: 4.0}, func(ev Event) error {
		played = append(played, ev)
		return nil
	})
	if err != nil || len(played) != 4 {
		t.Fatalf("Replay: len=%d err=%v", len(played), err)
	}
	if err := store.Delete("日常领取"); err != nil {
		t.Fatal(err)
	}
	list, _ = store.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 remaining macro, got %d", len(list))
	}
}

func TestRecorderPauseResume(t *testing.T) {
	var rec Recorder
	rec.Start()
	rec.Record(Event{Kind: EventTouchDown, NX: 0.1, NY: 0.2})
	rec.Pause()
	time.Sleep(15 * time.Millisecond)
	rec.Record(Event{Kind: EventTouchMove, NX: 0.3, NY: 0.4}) // ignored while paused
	rec.Resume()
	rec.Record(Event{Kind: EventTouchUp})
	sc := rec.Stop("test_rec")
	if len(sc.Events) != 2 {
		t.Fatalf("expected 2 events (paused ignored), got %d", len(sc.Events))
	}
}
