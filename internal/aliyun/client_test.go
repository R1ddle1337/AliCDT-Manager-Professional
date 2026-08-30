package aliyun

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetCDTTrafficAndPreserveErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("Action") != "ListCdtInternetTraffic" || r.Form.Get("Signature") == "" {
			t.Fatalf("missing signed action: %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"TrafficDetails": []map[string]string{{"Traffic": "1073741824"}, {"Traffic": "536870912"}},
		})
	}))
	defer server.Close()
	client := NewClient("id", "secret", "cn-hongkong", "china")
	client.CDTEndpoint = server.URL
	client.Now = func() time.Time { return time.Unix(0, 0) }
	client.Nonce = func() string { return "nonce" }
	traffic, err := client.GetCDTTraffic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if traffic != 1.5 {
		t.Fatalf("expected 1.5 GB, got %v", traffic)
	}
}

func TestGetCDTTrafficRejectsMissingDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"RequestId":"test"}`))
	}))
	defer server.Close()
	client := NewClient("id", "secret", "cn-hongkong", "china")
	client.CDTEndpoint = server.URL
	if _, err := client.GetCDTTraffic(context.Background()); err == nil {
		t.Fatal("expected missing TrafficDetails error")
	}
}

func TestGetInstancesPaginatesAndNormalizesPublicIPs(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("Action") != "DescribeInstances" || r.Form.Get("Signature") == "" {
			t.Fatalf("missing signed DescribeInstances request: %v", r.Form)
		}
		requests++
		switch r.Form.Get("PageNumber") {
		case "1":
			_, _ = w.Write([]byte(`{"TotalCount":2,"Instances":{"Instance":[{"InstanceId":"i-one","InstanceName":"one","RegionId":"cn-hongkong","Status":"Running","InstanceType":"ecs.c7.large","InternetMaxBandwidthOut":100,"SpotStrategy":"NoSpot","PublicIpAddress":{"IpAddress":["203.0.113.10"]},"EipAddress":{"IpAddress":""}}]}}`))
		case "2":
			_, _ = w.Write([]byte(`{"TotalCount":2,"Instances":{"Instance":[{"InstanceId":"i-two","InstanceName":"two","RegionId":"cn-hongkong","Status":"Stopped","InstanceType":"ecs.g7.large","InternetMaxBandwidthOut":200,"SpotStrategy":"SpotAsPriceGo","PublicIpAddress":{"IpAddress":["198.51.100.11"]},"EipAddress":{"IpAddress":"198.51.100.12"}}]}}`))
		default:
			t.Fatalf("unexpected page %q", r.Form.Get("PageNumber"))
		}
	}))
	defer server.Close()
	client := NewClient("id", "secret", "cn-hongkong", "china")
	client.ECSEndpoint = server.URL
	instances, err := client.GetInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(instances) != 2 {
		t.Fatalf("expected two pages and instances, requests=%d instances=%+v", requests, instances)
	}
	if instances[0].PublicIP != "203.0.113.10" || instances[0].IsSpot {
		t.Fatalf("unexpected first instance: %+v", instances[0])
	}
	if instances[1].PublicIP != "198.51.100.12" || !instances[1].IsSpot {
		t.Fatalf("unexpected second instance: %+v", instances[1])
	}
}
