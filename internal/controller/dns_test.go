package controller

import (
	"context"
	"testing"
)

func TestDNSProviderAndManagedRecordLifecycle(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := store.CreateDNSProvider(context.Background(), CreateDNSProviderRequest{Name: "cf", Type: "cloudflare", Zone: "example.com", APIToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if provider.ID == "" || !provider.TokenConfigured {
		t.Fatalf("provider was not created: %+v", provider)
	}
	provider, err = store.UpdateDNSProvider(context.Background(), provider.ID, CreateDNSProviderRequest{Name: "cf-renamed", Type: "cloudflare", Zone: "example.com"})
	if err != nil || provider.Name != "cf-renamed" || !provider.TokenConfigured {
		t.Fatalf("provider secret was not retained: %+v err=%v", provider, err)
	}
	record, err := store.CreateDNSRecord(context.Background(), CreateDNSRecordRequest{ProviderID: provider.ID, Name: "relay", Type: "A", Value: "192.0.2.1", TTL: 60})
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "pending" {
		t.Fatalf("unexpected initial record state: %+v", record)
	}
	if err := store.DeleteDNSProvider(context.Background(), provider.ID); err == nil {
		t.Fatal("provider with managed records was deleted")
	}
	updated, err := store.UpdateDNSRecord(context.Background(), record.ID, CreateDNSRecordRequest{ProviderID: provider.ID, Name: "relay", Type: "A", Value: "192.0.2.2", TTL: 120})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Value != "192.0.2.2" || updated.TTL != 120 {
		t.Fatalf("record was not updated: %+v", updated)
	}
	if err := store.DeleteDNSRecord(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDNSProvider(context.Background(), provider.ID); err != nil {
		t.Fatal(err)
	}
}
