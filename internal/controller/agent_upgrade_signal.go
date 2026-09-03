package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"
)

const defaultAgentUpgradeRequestPath = "/app/data/agent-upgrade.request"

type agentUpgradeRequest struct {
	RequestID   string    `json:"request_id"`
	RequestedAt time.Time `json:"requested_at"`
}

func (s *Server) agentUpgradeRequestPath() string {
	if s.agentUpgradeRequestFile != "" {
		return s.agentUpgradeRequestFile
	}
	return defaultAgentUpgradeRequestPath
}

func (s *Server) signalAgentUpgrade(ctx context.Context) error {
	request := agentUpgradeRequest{RequestID: randomID("agent-upgrade"), RequestedAt: time.Now().UTC()}
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if err := writeUpdateFile(s.agentUpgradeRequestPath(), encoded, 0600); err != nil {
		return errors.New("宿主机 Agent 升级服务不可用，请检查 alicdt-agent-upgrade 单元")
	}
	return nil
}

func (s *Server) clearAgentUpgradeSignal() error {
	err := os.Remove(s.agentUpgradeRequestPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
