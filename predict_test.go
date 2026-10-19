package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestObserveKeepsOnlyChangedValues(t *testing.T) {
	store := newStateStore("")
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 100, 1000, now)
	store.observe("lh-1", 100, 1000, now.Add(10*time.Minute))
	store.observe("lh-1", 100, 1000, now.Add(20*time.Minute))
	store.observe("lh-1", 200, 1000, now.Add(time.Hour))

	stat := store.instances["lh-1"]
	if stat == nil {
		t.Fatal("instance was not recorded")
	}
	if len(stat.Points) != 2 {
		t.Fatalf("points = %d, want 2", len(stat.Points))
	}
	if stat.Points[1].Used != 200 {
		t.Fatalf("last point = %d, want 200", stat.Points[1].Used)
	}
	if !stat.LastChanged.Equal(now.Add(time.Hour)) {
		t.Fatalf("lastChanged = %v, want %v", stat.LastChanged, now.Add(time.Hour))
	}
}

func TestPredictRateAndProjection(t *testing.T) {
	store := newStateStore("")
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 0, 1000, start)
	store.observe("lh-1", 100, 1000, start.Add(time.Hour))
	store.observe("lh-1", 200, 1000, start.Add(2*time.Hour))

	now := start.Add(4 * time.Hour)
	store.observe("lh-1", 200, 1000, now)
	got := store.predict("lh-1", now, 7*24*time.Hour, 75*time.Minute)

	if got.RatePerHour != 100 {
		t.Fatalf("rate = %v, want 100", got.RatePerHour)
	}
	if !got.Stale {
		t.Fatal("data should be stale after 2h without changes")
	}
	if got.Used != 400 {
		t.Fatalf("predicted used = %d, want 400", got.Used)
	}
	if got.Percent != 40 {
		t.Fatalf("predicted percent = %v, want 40", got.Percent)
	}
}

func TestPredictIsConservativeWhenFresh(t *testing.T) {
	store := newStateStore("")
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 0, 1000, start)
	store.observe("lh-1", 100, 1000, start.Add(time.Hour))

	now := start.Add(time.Hour)
	got := store.predict("lh-1", now, 7*24*time.Hour, 75*time.Minute)
	if got.Stale {
		t.Fatal("fresh data must not be treated as stale")
	}
	if got.Used != 100 {
		t.Fatalf("predicted used = %d, want 100 without stale data", got.Used)
	}
}

func TestPredictUsesOnlyTheWindow(t *testing.T) {
	store := newStateStore("")
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	// Old burst that should fall outside the three hour window.
	store.observe("lh-1", 0, 1000, start)
	store.observe("lh-1", 800, 1000, start.Add(time.Hour))
	// Recent steady usage.
	store.observe("lh-1", 900, 1000, start.Add(10*time.Hour))
	store.observe("lh-1", 950, 1000, start.Add(11*time.Hour))

	now := start.Add(11 * time.Hour)
	got := store.predict("lh-1", now, 3*time.Hour, 75*time.Minute)
	if got.RatePerHour != 50 {
		t.Fatalf("rate = %v, want 50 from the window", got.RatePerHour)
	}
}

func TestObserveResetsOnPackageReset(t *testing.T) {
	store := newStateStore("")
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 900, 1000, now)
	store.observe("lh-1", 10, 1000, now.Add(time.Hour))

	stat := store.instances["lh-1"]
	if len(stat.Points) != 1 {
		t.Fatalf("points = %d, want 1 after reset", len(stat.Points))
	}
	if stat.LastUsed != 10 {
		t.Fatalf("last used = %d, want 10", stat.LastUsed)
	}
	if !stat.StartsAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("startsAt = %v, want reset time", stat.StartsAt)
	}
}

func TestObserveResetsOnPackageSizeChange(t *testing.T) {
	store := newStateStore("")
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 500, 1000, now)
	store.observe("lh-1", 500, 2000, now.Add(time.Hour))

	if points := len(store.instances["lh-1"].Points); points != 1 {
		t.Fatalf("points = %d, want 1 after package change", points)
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := newStateStore(path)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 25, 1000, now)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := loaded.instances["lh-1"]
	if stat == nil {
		t.Fatal("state was not persisted")
	}
	if stat.LastUsed != 25 || stat.TotalBytes != 1000 {
		t.Fatalf("loaded stat = %#v", stat)
	}
	if len(stat.Points) != 1 || !stat.Points[0].At.Equal(now) {
		t.Fatalf("loaded points = %#v", stat.Points)
	}
}

func TestEvaluateStopsOnStalePrediction(t *testing.T) {
	store := newStateStore("")
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 0, 1000, start)
	store.observe("lh-1", 100, 1000, start.Add(time.Hour))
	store.observe("lh-1", 200, 1000, start.Add(2*time.Hour))
	now := start.Add(4 * time.Hour)
	store.observe("lh-1", 200, 1000, now)

	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 200, TotalBytes: 1000, State: "RUNNING"}}
	opts := evaluateOptions{
		Threshold:  30,
		Execute:    true,
		Store:      store,
		Window:     7 * 24 * time.Hour,
		StaleAfter: 75 * time.Minute,
		Now:        now,
	}
	if err := evaluate(context.Background(), api, "lh-1", opts); err != nil {
		t.Fatal(err)
	}
	if !api.stop {
		t.Fatal("stale data with predicted usage above the threshold should stop the instance")
	}
}

func TestEvaluateIgnoresPredictionWhenFresh(t *testing.T) {
	store := newStateStore("")
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 0, 1000, start)
	now := start.Add(2 * time.Hour)
	store.observe("lh-1", 200, 1000, now)

	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 200, TotalBytes: 1000, State: "RUNNING"}}
	opts := evaluateOptions{
		Threshold:  30,
		Execute:    true,
		Store:      store,
		Window:     7 * 24 * time.Hour,
		StaleAfter: 75 * time.Minute,
		Now:        now,
	}
	if err := evaluate(context.Background(), api, "lh-1", opts); err != nil {
		t.Fatal(err)
	}
	if api.stop {
		t.Fatal("fresh data must not trigger prediction-based shutdown")
	}
}
