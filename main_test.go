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

func TestParseTargets(t *testing.T) {
	got, err := parseTargets("ap-seoul=lh-seoul-1,lh-seoul-2;ap-guangzhou=lh-gz-1", "ap-beijing", "ignored")
	if err != nil {
		t.Fatal(err)
	}
	want := []targetGroup{
		{Region: "ap-seoul", InstanceIDs: []string{"lh-seoul-1", "lh-seoul-2"}},
		{Region: "ap-guangzhou", InstanceIDs: []string{"lh-gz-1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargets() = %#v, want %#v", got, want)
	}
}

func TestParseTargetsFallsBackToLegacyVariables(t *testing.T) {
	got, err := parseTargets("", "ap-seoul", "lh-1,lh-2")
	if err != nil {
		t.Fatal(err)
	}
	want := []targetGroup{{Region: "ap-seoul", InstanceIDs: []string{"lh-1", "lh-2"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargets() = %#v, want %#v", got, want)
	}
}

type fakeAPI struct {
	usage Usage
	stop  bool
	start bool
}

func (f *fakeAPI) DescribeInstances(context.Context, string) (Usage, error) {
	return f.usage, nil
}

func (f *fakeAPI) StopInstance(context.Context, string) error {
	f.stop = true
	return nil
}

func (f *fakeAPI) StartInstance(context.Context, string) error {
	f.start = true
	return nil
}

func TestEvaluateDryRunDoesNotStop(t *testing.T) {
	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 96, TotalBytes: 100, State: "RUNNING"}}
	if err := evaluate(context.Background(), api, "lh-1", 95, false, false); err != nil {
		t.Fatal(err)
	}
	if api.stop {
		t.Fatal("dry run stopped the instance")
	}
}

func TestEvaluateStopsWhenEnabled(t *testing.T) {
	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 95, TotalBytes: 100, State: "RUNNING"}}
	if err := evaluate(context.Background(), api, "lh-1", 95, true, false); err != nil {
		t.Fatal(err)
	}
	if !api.stop {
		t.Fatal("threshold did not stop the instance")
	}
}

func TestEvaluateDoesNotStopBelowThreshold(t *testing.T) {
	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 94, TotalBytes: 100, State: "RUNNING"}}
	if err := evaluate(context.Background(), api, "lh-1", 95, true, false); err != nil {
		t.Fatal(err)
	}
	if api.stop {
		t.Fatal("usage below threshold stopped the instance")
	}
}

func TestEvaluateAutoStartsStoppedInstanceBelowThreshold(t *testing.T) {
	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 10, TotalBytes: 100, State: "STOPPED"}}
	if err := evaluate(context.Background(), api, "lh-1", 95, true, true); err != nil {
		t.Fatal(err)
	}
	if !api.start {
		t.Fatal("stopped instance was not started")
	}
}
