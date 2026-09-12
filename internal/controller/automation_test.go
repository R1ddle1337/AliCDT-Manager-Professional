package controller

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type telegramRoundTripper func(*http.Request) (*http.Response, error)

func (fn telegramRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestTelegramDisableAndChunking(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpdateSettings(context.Background(), []SettingUpdate{{Key: "tg_bot_token", Value: "secret-token"}, {Key: "tg_chat_id", Value: "chat"}, {Key: "tg_enabled", Value: "0"}}); err != nil {
		t.Fatal(err)
	}
	requests := 0
	service := NewCloudService(store)
	service.telegramHTTPClient = &http.Client{Transport: telegramRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests++
		body, _ := io.ReadAll(request.Body)
		if strings.Contains(string(body), "secret-token") {
			t.Fatalf("bot token leaked into request body")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})}
	if err := service.sendTelegram(context.Background(), "disabled"); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("disabled telegram sent %d requests", requests)
	}
	longMessage := strings.Repeat("通知", 4001)
	if err := service.sendTelegramWithOptions(context.Background(), longMessage, true); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("long telegram message used %d requests, want 3", requests)
	}
}

func TestTelegramRejectsInvalidAPISuccessResponse(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpdateSettings(context.Background(), []SettingUpdate{{Key: "tg_bot_token", Value: "secret-token"}, {Key: "tg_chat_id", Value: "chat"}}); err != nil {
		t.Fatal(err)
	}
	service := NewCloudService(store)
	service.telegramHTTPClient = &http.Client{Transport: telegramRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":false,"description":"bad request"}`)), Header: make(http.Header)}, nil
	})}
	err = service.sendTelegram(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "rejected") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unexpected telegram error: %v", err)
	}
}

func TestTelegramCollapsesDuplicateAutomationAlerts(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpdateSettings(context.Background(), []SettingUpdate{{Key: "tg_bot_token", Value: "secret-token"}, {Key: "tg_chat_id", Value: "chat"}}); err != nil {
		t.Fatal(err)
	}
	requests := 0
	service := NewCloudService(store)
	service.telegramHTTPClient = &http.Client{Transport: telegramRoundTripper(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})}
	if err := service.sendTelegram(context.Background(), "duplicate"); err != nil {
		t.Fatal(err)
	}
	if err := service.sendTelegram(context.Background(), "duplicate"); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("duplicate alert sent %d requests, want 1", requests)
	}
}

func TestDisabledDailyReportDoesNotRecordASentMessage(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpdateSettings(context.Background(), []SettingUpdate{{Key: "tg_enabled", Value: "0"}}); err != nil {
		t.Fatal(err)
	}
	service := NewCloudService(store)
	if err := service.SendDailyReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	logs, err := store.ListSystemLogs(context.Background(), "system", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range logs {
		if strings.Contains(log.Message, "每日流量汇报已发送") {
			t.Fatal("disabled daily report was recorded as sent")
		}
	}
}

func TestPublicSettingsMaskTelegramTokenAndEmptySavePreservesIt(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.UpdateSettings(ctx, []SettingUpdate{{Key: "tg_bot_token", Value: "secret-token"}, {Key: "tg_chat_id", Value: "chat"}}); err != nil {
		t.Fatal(err)
	}
	settings, err := store.GetPublicSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := settings["tg_bot_token"]; ok {
		t.Fatal("telegram token was returned by public settings")
	}
	if settings["tg_configured"] != "1" {
		t.Fatalf("expected configured marker, got %#v", settings)
	}
	if err := store.UpdateSettings(ctx, []SettingUpdate{{Key: "tg_bot_token", Value: ""}, {Key: "tg_chat_id", Value: "new-chat"}}); err != nil {
		t.Fatal(err)
	}
	token, err := store.GetSetting(ctx, "tg_bot_token")
	if err != nil || token != "secret-token" {
		t.Fatalf("empty token save changed secret: %q, %v", token, err)
	}
}
