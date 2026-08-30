package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultUpdateRequestPath = "/app/data/update.request"
	defaultUpdateStatusPath  = "/app/data/update.status.json"
)

type UpdateStatus struct {
	Status       string    `json:"status"`
	Message      string    `json:"message,omitempty"`
	RequestID    string    `json:"request_id,omitempty"`
	TargetCommit string    `json:"target_commit,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
}

type updateRequest struct {
	RequestID   string    `json:"request_id"`
	RequestedAt time.Time `json:"requested_at"`
}

func (s *Server) requestSystemUpdate(w http.ResponseWriter, r *http.Request) {
	status, err := s.readUpdateStatus()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if status.Status == "pending" || status.Status == "running" {
		writeJSON(w, http.StatusConflict, status)
		return
	}
	request := updateRequest{RequestID: randomID("update"), RequestedAt: time.Now().UTC()}
	encoded, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := writeUpdateFile(s.updateRequestPath(), encoded, 0600); err != nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("宿主机更新服务不可用，请检查 systemd 更新单元"))
		return
	}
	status = UpdateStatus{Status: "pending", Message: "更新请求已提交，宿主机正在准备更新", RequestID: request.RequestID, StartedAt: request.RequestedAt}
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) systemUpdateStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := s.readUpdateStatus()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) updateRequestPath() string {
	if s.updateRequestFile != "" {
		return s.updateRequestFile
	}
	return defaultUpdateRequestPath
}

func (s *Server) updateStatusPath() string {
	if s.updateStatusFile != "" {
		return s.updateStatusFile
	}
	return defaultUpdateStatusPath
}

func (s *Server) readUpdateStatus() (UpdateStatus, error) {
	data, err := os.ReadFile(s.updateStatusPath())
	if errors.Is(err, os.ErrNotExist) {
		return UpdateStatus{Status: "idle", Message: "暂无更新任务"}, nil
	}
	if err != nil {
		return UpdateStatus{}, err
	}
	var status UpdateStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return UpdateStatus{}, errors.New("更新状态文件格式无效")
	}
	status.Status = strings.ToLower(strings.TrimSpace(status.Status))
	if status.Status == "" {
		status.Status = "idle"
	}
	return status, nil
}

func writeUpdateFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".update-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
