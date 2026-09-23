package main

import (
	"context"
	"reflect"
	"testing"
)

func TestSplitInstanceIDs(t *testing.T) {
	got := splitInstanceIDs("lh-1, lh-2,lh-1,,")
	want := []string{"lh-1", "lh-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitInstanceIDs() = %#v, want %#v", got, want)
	}
}

type fakeAPI struct {
	usage Usage
	stop  bool
}

func (f *fakeAPI) DescribeInstances(context.Context, string) (Usage, error) {
	return f.usage, nil
}

func (f *fakeAPI) StopInstance(context.Context, string) error {
	f.stop = true
	return nil
}

func TestEvaluateDryRunDoesNotStop(t *testing.T) {
	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 96, TotalBytes: 100, State: "RUNNING"}}
	if err := evaluate(context.Background(), api, "lh-1", 95, false); err != nil {
		t.Fatal(err)
	}
	if api.stop {
		t.Fatal("dry run stopped the instance")
	}
}

func TestEvaluateStopsWhenEnabled(t *testing.T) {
	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 95, TotalBytes: 100, State: "RUNNING"}}
	if err := evaluate(context.Background(), api, "lh-1", 95, true); err != nil {
		t.Fatal(err)
	}
	if !api.stop {
		t.Fatal("threshold did not stop the instance")
	}
}

func TestEvaluateDoesNotStopBelowThreshold(t *testing.T) {
	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 94, TotalBytes: 100, State: "RUNNING"}}
	if err := evaluate(context.Background(), api, "lh-1", 95, true); err != nil {
		t.Fatal(err)
	}
	if api.stop {
		t.Fatal("usage below threshold stopped the instance")
	}
}
