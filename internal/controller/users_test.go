package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestConsoleUserLifecycleUsageAndAuthorization(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	account, err := store.CreateCloudAccount(ctx, CloudAccountRequest{
		Name: "customer-cdt", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
		TrafficLimitGB: 200, ThresholdPercent: 95,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO account_traffic_snapshots(account_id,used_gb,synced_at,last_error,updated_at) VALUES(?,75,datetime('now'),'','now')`, account.ID); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, ServerOptions{AdminToken: "admin-secret"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	var user ConsoleUser
	requestJSON(t, httpServer.URL+"/api/v2/users", "admin-secret", ConsoleUserRequest{
		Username: "alice", DisplayName: "Alice", Password: "password-123", TrafficLimitGB: 100, AccountIDs: []int64{account.ID},
	}, http.StatusCreated, &user)
	if user.ID == 0 || user.TrafficUsedGB != 75 || user.TrafficPercent != 75 || !user.TrafficKnown || len(user.Accounts) != 1 {
		t.Fatalf("unexpected user: %+v", user)
	}

	var login struct {
		Token string `json:"token"`
		Role  string `json:"role"`
	}
	requestJSON(t, httpServer.URL+"/api/v2/auth/login", "", map[string]string{"username": "ALICE", "password": "password-123"}, http.StatusOK, &login)
	if login.Token == "" || login.Role != consoleRoleUser {
		t.Fatalf("unexpected login: %+v", login)
	}
	var overview ConsoleUser
	getJSON(t, httpServer.URL+"/api/v2/user/overview", login.Token, http.StatusOK, &overview)
	if overview.ID != user.ID || overview.TrafficRemainingGB != 25 || overview.Accounts[0].Name != "customer-cdt" {
		t.Fatalf("unexpected user overview: %+v", overview)
	}
	getJSON(t, httpServer.URL+"/api/v2/cloud/overview", login.Token, http.StatusForbidden, nil)

	disabled := false
	update := ConsoleUserRequest{
		Username: "alice", DisplayName: "Alice Example", TrafficLimitGB: 150, Enabled: &disabled, AccountIDs: []int64{account.ID},
	}
	putJSON(t, httpServer.URL+"/api/v2/users/"+jsonNumber(user.ID), "admin-secret", update, http.StatusOK, &user)
	if user.Enabled || user.DisplayName != "Alice Example" || user.TrafficLimitGB != 150 {
		t.Fatalf("unexpected updated user: %+v", user)
	}
	getJSON(t, httpServer.URL+"/api/v2/user/overview", login.Token, http.StatusUnauthorized, nil)
	requestJSON(t, httpServer.URL+"/api/v2/auth/login", "", map[string]string{"username": "alice", "password": "password-123"}, http.StatusUnauthorized, nil)
}

func TestConsoleUserRejectsDuplicateAccountAssignment(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.CreateCloudAccount(context.Background(), CloudAccountRequest{
		Name: "shared", AccessKeyID: "key", AccessKeySecret: "secret", RegionID: "cn-hongkong", SiteType: "china",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConsoleUser(context.Background(), ConsoleUserRequest{Username: "first", Password: "password-1", TrafficLimitGB: 100, AccountIDs: []int64{account.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConsoleUser(context.Background(), ConsoleUserRequest{Username: "second", Password: "password-2", TrafficLimitGB: 100, AccountIDs: []int64{account.ID}}); err == nil {
		t.Fatal("expected duplicate cloud account assignment to be rejected")
	}
}

func putJSON(t *testing.T, url, token string, payload interface{}, expected int, output interface{}) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	doJSON(t, request, expected, output)
}

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}
