package aliyun

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultCDTEndpoint = "https://cdt.aliyuncs.com/"
)

type Client struct {
	AccessKeyID     string
	AccessKeySecret string
	RegionID        string
	SiteType        string
	HTTPClient      *http.Client
	CDTEndpoint     string
	ECSEndpoint     string
	Now             func() time.Time
	Nonce           func() string
}

type APIError struct {
	Code      string
	Message   string
	RequestID string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Aliyun API error [%s]: %s", e.Code, e.Message)
}

type Instance struct {
	InstanceID    string `json:"instance_id"`
	InstanceName  string `json:"instance_name"`
	RegionID      string `json:"region_id"`
	Status        string `json:"status"`
	PublicIP      string `json:"public_ip"`
	InstanceType  string `json:"instance_type"`
	BandwidthMbps int    `json:"bandwidth_mbps"`
	IsSpot        bool   `json:"is_spot"`
}

func NewClient(accessKeyID, accessKeySecret, regionID, siteType string) *Client {
	return &Client{
		AccessKeyID: accessKeyID, AccessKeySecret: accessKeySecret,
		RegionID: regionID, SiteType: siteType,
		HTTPClient:  &http.Client{Timeout: 15 * time.Second},
		CDTEndpoint: defaultCDTEndpoint,
		ECSEndpoint: fmt.Sprintf("https://ecs.%s.aliyuncs.com/", regionID),
		Now:         time.Now,
		Nonce: func() string {
			return strconv.FormatInt(time.Now().UnixNano(), 36)
		},
	}
}

func (c *Client) GetCDTTraffic(ctx context.Context) (float64, error) {
	var response struct {
		Code           interface{} `json:"Code"`
		Message        string      `json:"Message"`
		RequestID      string      `json:"RequestId"`
		TrafficDetails []struct {
			Traffic interface{} `json:"Traffic"`
		} `json:"TrafficDetails"`
	}
	raw, err := c.call(ctx, c.CDTEndpoint, "ListCdtInternetTraffic", "2021-08-13", nil)
	if err != nil {
		return 0, err
	}
	if _, exists := raw["TrafficDetails"]; !exists {
		return 0, errors.New("CDT response missing TrafficDetails")
	}
	encoded, _ := json.Marshal(raw)
	if err := json.Unmarshal(encoded, &response); err != nil {
		return 0, err
	}
	var totalBytes float64
	for _, detail := range response.TrafficDetails {
		value, err := number(detail.Traffic)
		if err != nil {
			return 0, fmt.Errorf("invalid CDT traffic value: %w", err)
		}
		totalBytes += value
	}
	return round(totalBytes/(1024*1024*1024), 3), nil
}

func (c *Client) GetInstances(ctx context.Context) ([]Instance, error) {
	instances := make([]Instance, 0)
	for page := 1; page <= 1000; page++ {
		extra := map[string]string{
			"RegionId":   c.RegionID,
			"PageSize":   "100",
			"PageNumber": strconv.Itoa(page),
		}
		raw, err := c.call(ctx, c.ECSEndpoint, "DescribeInstances", "2014-05-26", extra)
		if err != nil {
			return nil, err
		}
		var response struct {
			TotalCount int `json:"TotalCount"`
			Instances  struct {
				Items []struct {
					InstanceID   string `json:"InstanceId"`
					InstanceName string `json:"InstanceName"`
					RegionID     string `json:"RegionId"`
					Status       string `json:"Status"`
					InstanceType string `json:"InstanceType"`
					SpotStrategy string `json:"SpotStrategy"`
					Bandwidth    int    `json:"InternetMaxBandwidthOut"`
					PublicIP     struct {
						Items []string `json:"IpAddress"`
					} `json:"PublicIpAddress"`
					EIP struct {
						Address string `json:"IpAddress"`
					} `json:"EipAddress"`
				} `json:"Instance"`
			} `json:"Instances"`
		}
		encoded, _ := json.Marshal(raw)
		if err := json.Unmarshal(encoded, &response); err != nil {
			return nil, err
		}
		for _, item := range response.Instances.Items {
			publicIP := item.EIP.Address
			if publicIP == "" && len(item.PublicIP.Items) > 0 {
				publicIP = item.PublicIP.Items[0]
			}
			instances = append(instances, Instance{
				InstanceID: item.InstanceID, InstanceName: item.InstanceName,
				RegionID: item.RegionID, Status: item.Status, PublicIP: publicIP,
				InstanceType: item.InstanceType, BandwidthMbps: item.Bandwidth,
				IsSpot: item.SpotStrategy != "" && item.SpotStrategy != "NoSpot",
			})
		}
		if len(response.Instances.Items) == 0 || (response.TotalCount > 0 && len(instances) >= response.TotalCount) || (response.TotalCount == 0 && len(response.Instances.Items) < 100) {
			break
		}
	}
	return instances, nil
}

func (c *Client) StartInstance(ctx context.Context, instanceID string) error {
	_, err := c.call(ctx, c.ECSEndpoint, "StartInstance", "2014-05-26", map[string]string{"InstanceId": instanceID})
	return err
}

func (c *Client) StopInstance(ctx context.Context, instanceID, mode string) error {
	if mode == "" {
		mode = "StopCharging"
	}
	_, err := c.call(ctx, c.ECSEndpoint, "StopInstance", "2014-05-26", map[string]string{
		"InstanceId": instanceID, "StoppedMode": mode, "ForceStop": "false",
	})
	return err
}

func (c *Client) call(ctx context.Context, endpoint, action, version string, extra map[string]string) (map[string]interface{}, error) {
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Nonce == nil {
		c.Nonce = func() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }
	}
	parameters := map[string]string{
		"Format": "JSON", "Version": version, "AccessKeyId": c.AccessKeyID,
		"SignatureMethod": "HMAC-SHA1", "Timestamp": c.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"SignatureVersion": "1.0", "SignatureNonce": c.Nonce(), "Action": action,
	}
	for key, value := range extra {
		parameters[key] = value
	}
	canonical := canonicalQuery(parameters)
	stringToSign := "POST&%2F&" + percentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(c.AccessKeySecret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	parameters["Signature"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))

	form := url.Values{}
	for key, value := range parameters {
		form.Set(key, value)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode Aliyun response: %w", err)
	}
	code := stringValue(payload["Code"])
	if code != "" && code != "200" && code != "0" && !strings.EqualFold(code, "success") && !strings.EqualFold(code, "true") {
		return nil, &APIError{Code: code, Message: stringValue(payload["Message"]), RequestID: stringValue(payload["RequestId"])}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Aliyun endpoint returned HTTP %d", response.StatusCode)
	}
	return payload, nil
}

func canonicalQuery(parameters map[string]string) string {
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, percentEncode(key)+"="+percentEncode(parameters[key]))
	}
	return strings.Join(parts, "&")
}

func percentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func number(value interface{}) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case json.Number:
		return typed.Float64()
	case string:
		if typed == "" {
			return 0, nil
		}
		return strconv.ParseFloat(typed, 64)
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("unsupported number type %T", value)
	}
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func round(value float64, places int) float64 {
	factor := 1.0
	for i := 0; i < places; i++ {
		factor *= 10
	}
	return float64(int64(value*factor+0.5)) / factor
}
