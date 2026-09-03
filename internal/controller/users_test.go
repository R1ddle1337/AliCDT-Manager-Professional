package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/R1ddle1337/AliCDT-Manager-Professional/internal/protocol"
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

func TestAssignedRelayServiceReceivesUserQuotaAndReportsExactUsage(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	user, err := store.CreateConsoleUser(ctx, ConsoleUserRequest{Username: "metered", DisplayName: "Metered User", Password: "password-123", TrafficLimitGB: 1.5})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnrollmentToken(ctx, "meter-enroll", time.Hour); err != nil {
		t.Fatal(err)
	}
	agent, err := store.EnrollAgent(ctx, protocol.AgentEnrollmentRequest{Token: "meter-enroll", NodeName: "meter-relay"})
	if err != nil {
		t.Fatal(err)
	}
	landing, err := store.CreateLandingNode(ctx, CreateLandingNodeRequest{Name: "landing", Address: "127.0.0.1", Port: 443, Network: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := store.CreateRelayService(ctx, CreateRelayServiceRequest{
		RelayNodeID: agent.AgentID, UserID: &user.ID, Name: "metered-entry", ListenPort: 18443, Network: "tcp", Mode: "failover",
		BillingMode: protocol.BillingModeDownload, Targets: []CreateServiceTarget{{LandingNodeID: landing.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := store.AgentConfig(ctx, agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Services) != 1 || config.Services[0].BillingMode != protocol.BillingModeDownload || config.Services[0].TrafficLimitGB != 1.5 || config.Services[0].AccessBlocked {
		t.Fatalf("unexpected metered Agent config: %+v", config)
	}
	const billed = uint64(512 * 1024 * 1024)
	if err := store.UpdateHeartbeat(ctx, agent.AgentID, protocol.AgentHeartbeat{CurrentRevision: config.Revision, Services: []protocol.ServiceStatus{{
		ID: service.ID, Name: service.Name, Listening: true, BytesUp: 64, BytesDown: billed, BilledBytes: billed,
		BillingMode: protocol.BillingModeDownload, TrafficLimitGB: 1.5, BillingEpoch: service.BillingEpoch,
	}}}); err != nil {
		t.Fatal(err)
	}
	overview, err := store.ConsoleUserOverview(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if overview.TrafficSource != "agent" || !overview.TrafficKnown || overview.TrafficUsedGB != 0.5 || overview.BilledBytes != billed || overview.RelayService == nil {
		t.Fatalf("unexpected exact user usage: %+v", overview)
	}

	disabled := false
	if _, err := store.UpdateConsoleUser(ctx, user.ID, ConsoleUserRequest{Username: user.Username, DisplayName: user.DisplayName, Enabled: &disabled, TrafficLimitGB: user.TrafficLimitGB, AccountIDs: []int64{}}); err != nil {
		t.Fatal(err)
	}
	config, err = store.AgentConfig(ctx, agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if !config.Services[0].AccessBlocked {
		t.Fatalf("disabled user service remained accessible: %+v", config.Services[0])
	}
	reset, err := store.ResetRelayServiceTraffic(ctx, service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reset.BillingEpoch <= service.BillingEpoch {
		t.Fatalf("billing epoch = %d, want greater than %d", reset.BillingEpoch, service.BillingEpoch)
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
