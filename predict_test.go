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
	got := store.predict("lh-1", now)

	if got.RatePerHour != 100 {
		t.Fatalf("rate = %v, want 100", got.RatePerHour)
	}
	if got.Used != 400 {
		t.Fatalf("predicted used = %d, want 400", got.Used)
	}
	if got.Percent != 40 {
		t.Fatalf("predicted percent = %v, want 40", got.Percent)
	}
}

func TestPredictGrowsWithinRefreshInterval(t *testing.T) {
	store := newStateStore("")
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 0, 1000, start)
	store.observe("lh-1", 100, 1000, start.Add(time.Hour))

	// Thirty minutes after the last refresh the API still reports 100, but the
	// instance has been consuming traffic the whole time.
	now := start.Add(time.Hour + 30*time.Minute)
	store.observe("lh-1", 100, 1000, now)

	got := store.predict("lh-1", now)
	if got.RatePerHour != 100 {
		t.Fatalf("rate = %v, want 100", got.RatePerHour)
	}
	if got.Elapsed != 30*time.Minute {
		t.Fatalf("elapsed = %v, want 30m", got.Elapsed)
	}
	if got.Used != 150 {
		t.Fatalf("predicted used = %d, want 150", got.Used)
	}
}

func TestPredictEqualsActualRightAfterRefresh(t *testing.T) {
	store := newStateStore("")
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 0, 1000, start)

	now := start.Add(time.Hour)
	store.observe("lh-1", 100, 1000, now)

	got := store.predict("lh-1", now)
	if got.Elapsed != 0 {
		t.Fatalf("elapsed = %v, want 0 right after a refresh", got.Elapsed)
	}
	if got.Used != 100 {
		t.Fatalf("predicted used = %d, want 100", got.Used)
	}
}

func TestPredictAveragesSevenDays(t *testing.T) {
	store := newStateStore("")
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	// Eight days of hourly samples, 10 bytes each hour.
	const hours = 192
	for hour := 0; hour <= hours; hour++ {
		store.observe("lh-1", int64(hour*10), 10000, start.Add(time.Duration(hour)*time.Hour))
	}

	now := start.Add(hours * time.Hour)
	stat := store.instances["lh-1"]
	if len(stat.Points) > 8*24+1 {
		t.Fatalf("points = %d, history was not pruned", len(stat.Points))
	}
	if oldest := stat.Points[0]; oldest.At.Before(now.Add(-historyWindow - time.Hour)) {
		t.Fatalf("oldest point = %v, history exceeds the retention period", oldest.At)
	}
	got := store.predict("lh-1", now)
	if got.RatePerHour != 10 {
		t.Fatalf("rate = %v, want an average of 10 bytes per hour", got.RatePerHour)
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

func TestEvaluateDoesNotExtrapolateStoppedInstance(t *testing.T) {
	store := newStateStore("")
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 0, 1000, start)
	store.observe("lh-1", 100, 1000, start.Add(time.Hour))

	// A stopped instance keeps reporting 100 bytes, but it is not consuming
	// anything, so the history must not be projected forward.
	now := start.Add(time.Hour + 2*time.Hour)
	store.observe("lh-1", 100, 1000, now)

	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 100, TotalBytes: 1000, State: "STOPPED"}}
	opts := evaluateOptions{
		Threshold: 15,
		Execute:   true,
		AutoStart: true,
		Store:     store,
		Now:       now,
	}
	if err := evaluate(context.Background(), api, "lh-1", opts); err != nil {
		t.Fatal(err)
	}
	if !api.start {
		t.Fatal("stopped instance below the threshold should be started")
	}
	if api.stop {
		t.Fatal("stopped instance must not receive another stop request")
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

func TestEvaluateStopsOnPredictedUsage(t *testing.T) {
	store := newStateStore("")
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 0, 1000, start)
	store.observe("lh-1", 100, 1000, start.Add(time.Hour))

	// Only 100 bytes are reported, but 30 minutes of traffic at 100B/h have not
	// been reported yet, so the predicted usage reaches 150 bytes (15%).
	now := start.Add(time.Hour + 30*time.Minute)
	store.observe("lh-1", 100, 1000, now)

	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 100, TotalBytes: 1000, State: "RUNNING"}}
	opts := evaluateOptions{
		Threshold: 15,
		Execute:   true,
		Store:     store,
		Now:       now,
	}
	if err := evaluate(context.Background(), api, "lh-1", opts); err != nil {
		t.Fatal(err)
	}
	if !api.stop {
		t.Fatal("predicted usage above the threshold should stop the instance")
	}
}

func TestEvaluateDoesNotStopWhenPredictionIsBelowThreshold(t *testing.T) {
	store := newStateStore("")
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store.observe("lh-1", 0, 1000, start)
	store.observe("lh-1", 100, 1000, start.Add(time.Hour))

	// Reported usage is 10%, predicted usage 15%, threshold 20%.
	now := start.Add(time.Hour + 30*time.Minute)
	store.observe("lh-1", 100, 1000, now)

	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 100, TotalBytes: 1000, State: "RUNNING"}}
	opts := evaluateOptions{
		Threshold: 20,
		Execute:   true,
		Store:     store,
		Now:       now,
	}
	if err := evaluate(context.Background(), api, "lh-1", opts); err != nil {
		t.Fatal(err)
	}
	if api.stop {
		t.Fatal("predicted usage below the threshold must not stop the instance")
	}
}
