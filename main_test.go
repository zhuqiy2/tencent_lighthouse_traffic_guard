package main

import (
	"context"
	"fmt"
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

func TestParseTargetsAll(t *testing.T) {
	got, err := parseTargets("ap-seoul=all;ap-guangzhou=lh-gz-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []targetGroup{
		{Region: "ap-seoul", AutoDiscover: true},
		{Region: "ap-guangzhou", InstanceIDs: []string{"lh-gz-1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargets() = %#v, want %#v", got, want)
	}
}

func TestParseTargetsAllFromLegacyVariable(t *testing.T) {
	got, err := parseTargets("", "ap-seoul", "ALL")
	if err != nil {
		t.Fatal(err)
	}
	want := []targetGroup{{Region: "ap-seoul", AutoDiscover: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargets() = %#v, want %#v", got, want)
	}
}

func TestParseTargetsRejectsAllMixedWithIDs(t *testing.T) {
	if _, err := parseTargets("ap-seoul=all,lh-1", "", ""); err == nil {
		t.Fatal("expected mixed all and explicit IDs to be rejected")
	}
}

func TestParseTargetsAllRegions(t *testing.T) {
	got, err := parseTargets("*=all", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []targetGroup{{Region: "*", AutoDiscover: true, AllRegions: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargets() = %#v, want %#v", got, want)
	}
}

func TestParseTargetsAllRegionsFromLegacyVariables(t *testing.T) {
	got, err := parseTargets("", "*", "all")
	if err != nil {
		t.Fatal(err)
	}
	want := []targetGroup{{Region: "*", AutoDiscover: true, AllRegions: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargets() = %#v, want %#v", got, want)
	}
}

func TestParseTargetsRejectsAllRegionsWithExplicitIDs(t *testing.T) {
	if _, err := parseTargets("*=lh-1", "", ""); err == nil {
		t.Fatal("expected all-regions with explicit IDs to be rejected")
	}
	if _, err := parseTargets("all=lh-1", "", ""); err == nil {
		t.Fatal("expected all-regions with explicit IDs to be rejected")
	}
}

func TestParseTargetsAllRegionsSpelledAll(t *testing.T) {
	got, err := parseTargets("", "all", "all")
	if err != nil {
		t.Fatal(err)
	}
	want := []targetGroup{{Region: "*", AutoDiscover: true, AllRegions: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargets() = %#v, want %#v", got, want)
	}

	got, err = parseTargets("all=all", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargets() = %#v, want %#v", got, want)
	}

	got, err = parseTargets("ALL=ALL", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTargets() = %#v, want %#v", got, want)
	}
}

func TestMergeRegionTargetsKeepsExplicitAndDeduplicates(t *testing.T) {
	existing := []targetGroup{{Region: "ap-seoul", InstanceIDs: []string{"lh-1"}}}
	got := mergeRegionTargets(existing, []string{"ap-seoul", "ap-guangzhou"})
	want := []targetGroup{
		{Region: "ap-seoul", InstanceIDs: []string{"lh-1"}},
		{Region: "ap-guangzhou", AutoDiscover: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeRegionTargets() = %#v, want %#v", got, want)
	}
}

type fakeAPI struct {
	usage      Usage
	stop       bool
	start      bool
	discovered []string
	noTraffic  bool
}

func (f *fakeAPI) DescribeInstances(_ context.Context, instanceID string) (Usage, error) {
	if f.noTraffic {
		return Usage{}, fmt.Errorf("%w for instance %s", errNoTrafficPackage, instanceID)
	}
	return f.usage, nil
}

func (f *fakeAPI) ListInstanceIDs(context.Context) ([]string, error) {
	return f.discovered, nil
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
	if err := evaluate(context.Background(), api, "lh-1", evaluateOptions{Threshold: 95}); err != nil {
		t.Fatal(err)
	}
	if api.stop {
		t.Fatal("dry run stopped the instance")
	}
}

func TestEvaluateStopsWhenEnabled(t *testing.T) {
	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 95, TotalBytes: 100, State: "RUNNING"}}
	if err := evaluate(context.Background(), api, "lh-1", evaluateOptions{Threshold: 95, Execute: true}); err != nil {
		t.Fatal(err)
	}
	if !api.stop {
		t.Fatal("threshold did not stop the instance")
	}
}

func TestEvaluateDoesNotStopBelowThreshold(t *testing.T) {
	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 94, TotalBytes: 100, State: "RUNNING"}}
	if err := evaluate(context.Background(), api, "lh-1", evaluateOptions{Threshold: 95, Execute: true}); err != nil {
		t.Fatal(err)
	}
	if api.stop {
		t.Fatal("usage below threshold stopped the instance")
	}
}

func TestEvaluateAutoStartsStoppedInstanceBelowThreshold(t *testing.T) {
	api := &fakeAPI{usage: Usage{InstanceID: "lh-1", UsedBytes: 10, TotalBytes: 100, State: "STOPPED"}}
	if err := evaluate(context.Background(), api, "lh-1", evaluateOptions{Threshold: 95, Execute: true, AutoStart: true}); err != nil {
		t.Fatal(err)
	}
	if !api.start {
		t.Fatal("stopped instance was not started")
	}
}

func TestRunTargetSkipsInstancesWithoutTrafficPackage(t *testing.T) {
	api := &fakeAPI{noTraffic: true, discovered: []string{"lh-1", "lh-2"}}
	target := targetGroup{Region: "ap-seoul", AutoDiscover: true}
	if failed := runTarget(context.Background(), api, target, evaluateOptions{Threshold: 95, Execute: true}); failed {
		t.Fatal("instances without traffic packages should not mark the run as failed")
	}
	if api.stop || api.start {
		t.Fatal("instances without traffic packages must not change state")
	}
}

func TestRunTargetExplicitInstanceWithoutTrafficPackageFails(t *testing.T) {
	api := &fakeAPI{noTraffic: true}
	target := targetGroup{Region: "ap-seoul", InstanceIDs: []string{"lh-1"}}
	if failed := runTarget(context.Background(), api, target, evaluateOptions{Threshold: 95, Execute: true}); !failed {
		t.Fatal("explicit instance without traffic package should be reported as failure")
	}
}
