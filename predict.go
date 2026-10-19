package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	stateVersion      = 1
	maxSamplePoints   = 5000
	maxPredictHorizon = 24 * time.Hour
	statePruneAfter   = 45 * 24 * time.Hour
)

const defaultStateFile = "lighthouse-traffic-state.json"

// samplePoint records a traffic package value that actually changed.
// Tencent Cloud refreshes traffic usage roughly once per hour, so polling more
// often does not produce extra points.
type samplePoint struct {
	At   time.Time `json:"at"`
	Used int64     `json:"used"`
}

type instanceStat struct {
	TotalBytes  int64         `json:"totalBytes"`
	LastUsed    int64         `json:"lastUsed"`
	LastSeen    time.Time     `json:"lastSeen"`
	LastChanged time.Time     `json:"lastChanged"`
	StartsAt    time.Time     `json:"startsAt"`
	Points      []samplePoint `json:"points"`
}

type stateFile struct {
	Version   int                      `json:"version"`
	Instances map[string]*instanceStat `json:"instances"`
}

// stateStore keeps the traffic history used to predict usage while the
// Tencent Cloud API has not refreshed its data yet.
type stateStore struct {
	path      string
	instances map[string]*instanceStat
	dirty     bool
}

// prediction is what we expect the traffic package to look like right now.
type prediction struct {
	Used        int64
	Percent     float64
	RatePerHour float64
	StaleFor    time.Duration
	Stale       bool
	Points      int
}

func newStateStore(path string) *stateStore {
	return &stateStore{path: path, instances: make(map[string]*instanceStat)}
}

func loadState(path string) (*stateStore, error) {
	store := newStateStore(path)
	if path == "" {
		return store, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}
	var file stateFile
	if err := json.Unmarshal(content, &file); err != nil {
		return nil, fmt.Errorf("parse state file: %w", err)
	}
	if file.Instances != nil {
		store.instances = file.Instances
	}
	return store, nil
}

func (s *stateStore) save() error {
	if s == nil || s.path == "" || !s.dirty {
		return nil
	}
	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create state directory: %w", err)
		}
	}
	data, err := json.MarshalIndent(stateFile{Version: stateVersion, Instances: s.instances}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state file: %w", err)
	}
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	s.dirty = false
	return nil
}

// observe records one API observation. Only changed values become sample
// points, which keeps the history compact and matches the hourly refresh rate.
func (s *stateStore) observe(instanceID string, used, total int64, now time.Time) {
	if s == nil || instanceID == "" {
		return
	}
	if s.instances == nil {
		s.instances = make(map[string]*instanceStat)
	}
	stat := s.instances[instanceID]
	if stat == nil {
		stat = &instanceStat{StartsAt: now}
		s.instances[instanceID] = stat
	}
	// A smaller value or a different package size means the package was reset.
	if stat.TotalBytes != total || (len(stat.Points) > 0 && used < stat.LastUsed) {
		stat.Points = nil
		stat.StartsAt = now
		stat.LastChanged = now
	}
	if len(stat.Points) == 0 {
		stat.Points = []samplePoint{{At: now, Used: used}}
		stat.LastChanged = now
	} else if used != stat.LastUsed {
		stat.Points = append(stat.Points, samplePoint{At: now, Used: used})
		stat.LastChanged = now
	}
	stat.TotalBytes = total
	stat.LastUsed = used
	stat.LastSeen = now
	stat.prune()
	s.dirty = true
}

// prune caps the number of sample points. Points are kept regardless of age so
// that a larger window configured later still has data; the window only filters
// samples when computing the rate.
func (st *instanceStat) prune() {
	if len(st.Points) > maxSamplePoints {
		st.Points = append(st.Points[:0], st.Points[len(st.Points)-maxSamplePoints:]...)
	}
}

// predict extrapolates the current sample using the average rate observed in
// the history window. Extrapolation only starts once the data is stale, and it
// is capped at one day to avoid absurd values after a long outage.
func (s *stateStore) predict(instanceID string, now time.Time, window, staleAfter time.Duration) prediction {
	var result prediction
	if s == nil {
		return result
	}
	stat := s.instances[instanceID]
	if stat == nil || stat.TotalBytes <= 0 {
		return result
	}
	result.Points = len(stat.Points)
	points := stat.Points
	if len(points) > 1 {
		cutoff := now.Add(-window)
		index := sort.Search(len(points), func(i int) bool {
			return !points[i].At.Before(cutoff)
		})
		if index >= len(points) {
			index = len(points) - 1
		}
		points = points[index:]
	}
	if len(points) >= 2 {
		first, last := points[0], points[len(points)-1]
		if elapsed := last.At.Sub(first.At); elapsed > 0 && last.Used >= first.Used {
			result.RatePerHour = float64(last.Used-first.Used) / elapsed.Hours()
		}
	}
	result.StaleFor = now.Sub(stat.LastChanged)
	if result.StaleFor < 0 {
		result.StaleFor = 0
	}
	result.Stale = !stat.LastChanged.IsZero() && staleAfter > 0 && result.StaleFor >= staleAfter
	result.Used = stat.LastUsed
	if result.RatePerHour > 0 && result.Stale {
		horizon := result.StaleFor
		if horizon > maxPredictHorizon {
			horizon = maxPredictHorizon
		}
		predicted := float64(stat.LastUsed) + result.RatePerHour*horizon.Hours()
		if predicted > float64(result.Used) {
			result.Used = int64(predicted)
		}
	}
	result.Percent = float64(result.Used) / float64(stat.TotalBytes) * 100
	return result
}

// pruneInstances forgets instances that have not been seen for a long time.
func (s *stateStore) pruneInstances(now time.Time) {
	if s == nil {
		return
	}
	for instanceID, stat := range s.instances {
		if stat == nil || now.Sub(stat.LastSeen) > statePruneAfter {
			delete(s.instances, instanceID)
			s.dirty = true
		}
	}
}

func humanBytes(value int64) string {
	if value < 0 {
		return "0B"
	}
	if value < 1024 {
		return fmt.Sprintf("%dB", value)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	size := float64(value)
	index := -1
	for size >= 1024 && index < len(units)-1 {
		size /= 1024
		index++
	}
	return fmt.Sprintf("%.2f%s", size, units[index])
}
