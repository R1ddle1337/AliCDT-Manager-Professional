// Package dnsprovider contains the provider-neutral DNS reconciliation API.
//
// The controller deliberately keeps DNS credentials and vendor-specific API
// details out of relay scheduling.  A relay pool only produces a desired set
// of records; a provider reconciles that set with the DNS service.
package dnsprovider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type Record struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   int    `json:"ttl"`
}

type DesiredRecord struct {
	Name             string
	Type             string
	Value            string
	TTL              int
	ProviderRecordID string
}

type RecordScope struct {
	Name string
	Type string
}

type Config struct {
	Type            string
	Zone            string
	ZoneID          string
	Endpoint        string
	AccessKeyID     string
	AccessKeySecret string
	APIToken        string
	APIEmail        string
	HTTPClient      *http.Client
}

type Capabilities struct {
	WeightedRecords bool `json:"weighted_records"`
	HealthChecks    bool `json:"health_checks"`
}

type SyncResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Deleted int      `json:"deleted"`
	Skipped int      `json:"skipped"`
	Records []Record `json:"records"`
}

// Provider is intentionally small.  New DNS vendors only need to implement
// these methods; the controller's reconciliation and UI remain unchanged.
type Provider interface {
	Type() string
	Capabilities() Capabilities
	Test(context.Context) error
	ListRecords(context.Context, string, string, string) ([]Record, error)
	EnsureRecords(context.Context, string, []RecordScope, []DesiredRecord) (SyncResult, error)
	DeleteRecord(context.Context, string, string) error
}

// FQDN normalizes a record name against a provider zone. It is exported for
// controller reconciliation and UI diagnostics.
func FQDN(name, zone string) string { return fqdn(name, zone) }

func fqdn(name, zone string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	zone = strings.TrimSuffix(strings.TrimSpace(zone), ".")
	if name == "@" || name == "" {
		return zone
	}
	if strings.HasSuffix(name, "."+zone) {
		return name
	}
	return name + "." + zone
}

func New(cfg Config) (Provider, error) {
	cfg.Type = strings.ToLower(strings.TrimSpace(cfg.Type))
	cfg.Zone = strings.TrimSpace(cfg.Zone)
	if cfg.Zone == "" {
		return nil, errors.New("dns zone is required")
	}
	switch cfg.Type {
	case "aliyun", "alibaba", "alidns":
		endpoint, err := normalizeProviderEndpoint(cfg.Endpoint, "https://alidns.aliyuncs.com/")
		if err != nil {
			return nil, err
		}
		cfg.Endpoint = endpoint
		return NewAliyun(cfg), nil
	case "cloudflare":
		endpoint, err := normalizeProviderEndpoint(cfg.Endpoint, "https://api.cloudflare.com/client/v4")
		if err != nil {
			return nil, err
		}
		cfg.Endpoint = endpoint
		return NewCloudflare(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported DNS provider %q", cfg.Type)
	}
}
