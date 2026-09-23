package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	lighthouse "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/lighthouse/v20200324"
)

type Usage struct {
	InstanceID string
	UsedBytes  int64
	TotalBytes int64
	State      string
}

type usageState struct {
	UsedBytes int64 `json:"usedBytes"`
}

func (u Usage) Percent() float64 {
	if u.TotalBytes <= 0 {
		return 0
	}
	return float64(u.UsedBytes) / float64(u.TotalBytes) * 100
}

type LighthouseAPI interface {
	DescribeInstances(ctx context.Context, instanceID string) (Usage, error)
	StopInstance(ctx context.Context, instanceID string) error
	StartInstance(ctx context.Context, instanceID string) error
}

type SDKClient struct {
	client *lighthouse.Client
}

type targetGroup struct {
	Region      string
	InstanceIDs []string
}

func (c *SDKClient) DescribeInstances(ctx context.Context, instanceID string) (Usage, error) {
	req := lighthouse.NewDescribeInstancesRequest()
	if instanceID != "" {
		req.InstanceIds = []*string{&instanceID}
	}
	resp, err := c.client.DescribeInstancesWithContext(ctx, req)
	if err != nil {
		return Usage{}, err
	}
	if resp.Response == nil || len(resp.Response.InstanceSet) != 1 {
		count := 0
		if resp.Response != nil {
			count = len(resp.Response.InstanceSet)
		}
		return Usage{}, fmt.Errorf("expected exactly one instance, got %d", count)
	}
	instance := resp.Response.InstanceSet[0]
	if instance == nil || instance.InstanceId == nil {
		return Usage{}, fmt.Errorf("instance response is incomplete")
	}
	trafficReq := lighthouse.NewDescribeInstancesTrafficPackagesRequest()
	trafficReq.InstanceIds = []*string{instance.InstanceId}
	trafficResp, err := c.client.DescribeInstancesTrafficPackagesWithContext(ctx, trafficReq)
	if err != nil {
		return Usage{}, err
	}
	if trafficResp.Response == nil || len(trafficResp.Response.InstanceTrafficPackageSet) != 1 || len(trafficResp.Response.InstanceTrafficPackageSet[0].TrafficPackageSet) == 0 {
		return Usage{}, fmt.Errorf("no traffic package found for instance %s", *instance.InstanceId)
	}
	traffic := trafficResp.Response.InstanceTrafficPackageSet[0].TrafficPackageSet[0]
	if traffic == nil || traffic.TrafficUsed == nil || traffic.TrafficPackageTotal == nil {
		return Usage{}, fmt.Errorf("traffic package response is incomplete")
	}
	return Usage{
		InstanceID: *instance.InstanceId,
		UsedBytes:  *traffic.TrafficUsed,
		TotalBytes: *traffic.TrafficPackageTotal,
		State:      valueOrEmpty(instance.InstanceState),
	}, nil
}

func (c *SDKClient) StopInstance(ctx context.Context, instanceID string) error {
	req := lighthouse.NewStopInstancesRequest()
	req.InstanceIds = []*string{&instanceID}
	_, err := c.client.StopInstancesWithContext(ctx, req)
	return err
}

func (c *SDKClient) StartInstance(ctx context.Context, instanceID string) error {
	req := lighthouse.NewStartInstancesRequest()
	req.InstanceIds = []*string{&instanceID}
	_, err := c.client.StartInstancesWithContext(ctx, req)
	return err
}

func evaluate(ctx context.Context, api LighthouseAPI, instanceID string, threshold float64, execute, autoStart bool) error {
	usage, err := api.DescribeInstances(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("query traffic usage: %w", err)
	}
	percent := usage.Percent()
	log.Printf("instance=%s state=%s used=%d total=%d usage=%.2f%% threshold=%.2f%%", usage.InstanceID, usage.State, usage.UsedBytes, usage.TotalBytes, percent, threshold)
	if autoStart && percent < threshold && (strings.EqualFold(usage.State, "SHUTDOWN") || strings.EqualFold(usage.State, "STOPPED")) {
		if !execute {
			log.Printf("traffic appears reset; dry run, add --execute to start the instance")
		} else if err := api.StartInstance(ctx, usage.InstanceID); err != nil {
			return fmt.Errorf("start instance: %w", err)
		} else {
			log.Printf("start request submitted for instance=%s", usage.InstanceID)
		}
	}
	if percent < threshold {
		return nil
	}
	if strings.EqualFold(usage.State, "SHUTDOWN") || strings.EqualFold(usage.State, "STOPPED") {
		log.Printf("instance already stopped; no action taken")
		return nil
	}
	if !execute {
		log.Printf("threshold exceeded; dry run, add --execute to stop the instance")
		return nil
	}
	if err := api.StopInstance(ctx, usage.InstanceID); err != nil {
		return fmt.Errorf("stop instance: %w", err)
	}
	log.Printf("stop request submitted for instance=%s", usage.InstanceID)
	return nil
}

func main() {
	envFile := findEnvFile(os.Args[1:])
	if envFile != "" {
		if err := loadEnvFile(envFile); err != nil {
			log.Fatal(err)
		}
	}
	flag.String("env-file", envFile, "load KEY=VALUE settings from this file before reading flags")
	instanceIDs := flag.String("instance-id", os.Getenv("TENCENTCLOUD_INSTANCE_ID"), "comma-separated Lighthouse instance IDs")
	region := flag.String("region", envOr("TENCENTCLOUD_REGION", "ap-guangzhou"), "Tencent Cloud region")
	threshold := flag.Float64("threshold", envFloat("TRAFFIC_THRESHOLD_PERCENT", 95), "shutdown threshold in percent")
	interval := flag.Duration("interval", 0, "repeat interval; 0 means run once")
	execute := flag.Bool("execute", false, "actually stop the instance when threshold is exceeded")
	autoStart := flag.Bool("auto-start", false, "start stopped instances when usage is below the threshold")
	flag.Parse()

	targets, err := parseTargets(os.Getenv("TENCENTCLOUD_TARGETS"), *region, *instanceIDs)
	if err != nil {
		log.Fatal(err)
	}
	if len(targets) == 0 {
		log.Fatal("missing -instance-id or TENCENTCLOUD_INSTANCE_ID")
	}
	if *threshold <= 0 || *threshold > 100 {
		log.Fatal("threshold must be greater than 0 and at most 100")
	}
	secretID := os.Getenv("TENCENTCLOUD_SECRET_ID")
	secretKey := os.Getenv("TENCENTCLOUD_SECRET_KEY")
	if secretID == "" || secretKey == "" {
		log.Fatal("set TENCENTCLOUD_SECRET_ID and TENCENTCLOUD_SECRET_KEY")
	}

	cred := common.NewCredential(secretID, secretKey)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "lighthouse.tencentcloudapi.com"
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		var failed bool
		for _, target := range targets {
			client, err := lighthouse.NewClient(cred, target.Region, cpf)
			if err != nil {
				log.Printf("region=%s create client: %v", target.Region, err)
				failed = true
				continue
			}
			api := &SDKClient{client: client}
			for _, instanceID := range target.InstanceIDs {
				if err := evaluate(ctx, api, instanceID, *threshold, *execute, *autoStart); err != nil {
					log.Print(err)
					failed = true
				}
			}
		}
		cancel()
		if failed {
			os.Exit(1)
		}
		if *interval <= 0 {
			return
		}
		time.Sleep(*interval)
	}
}

func parseTargets(value, fallbackRegion, fallbackIDs string) ([]targetGroup, error) {
	if strings.TrimSpace(value) == "" {
		ids := splitInstanceIDs(fallbackIDs)
		if len(ids) == 0 {
			return nil, nil
		}
		return []targetGroup{{Region: fallbackRegion, InstanceIDs: ids}}, nil
	}
	var targets []targetGroup
	seenRegions := make(map[string]struct{})
	for _, item := range strings.Split(value, ";") {
		region, idsValue, found := strings.Cut(item, "=")
		region = strings.TrimSpace(region)
		if !found || region == "" {
			return nil, fmt.Errorf("invalid target %q, expected region=instance-id[,instance-id]", item)
		}
		if _, exists := seenRegions[region]; exists {
			return nil, fmt.Errorf("duplicate region %q in TENCENTCLOUD_TARGETS", region)
		}
		ids := splitInstanceIDs(idsValue)
		if len(ids) == 0 {
			return nil, fmt.Errorf("region %q has no instance IDs", region)
		}
		seenRegions[region] = struct{}{}
		targets = append(targets, targetGroup{Region: region, InstanceIDs: ids})
	}
	return targets, nil
}

func splitInstanceIDs(value string) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		ids = append(ids, item)
	}
	return ids
}

func findEnvFile(args []string) string {
	for index, arg := range args {
		if arg == "--env-file" && index+1 < len(args) {
			return args[index+1]
		}
		if strings.HasPrefix(arg, "--env-file=") {
			return strings.TrimPrefix(arg, "--env-file=")
		}
	}
	return ""
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envFloat(name string, fallback float64) float64 {
	value, err := strconv.ParseFloat(os.Getenv(name), 64)
	if err != nil {
		return fallback
	}
	return value
}

func loadEnvFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read env file: %w", err)
	}
	for lineNumber, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) == "" {
			return fmt.Errorf("invalid env file line %d", lineNumber+1)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set env %s: %w", key, err)
		}
	}
	return nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
