package controller

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Server) legacyListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.store.ListCloudAccounts(r.Context(), false)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (s *Server) legacyCreateAccount(w http.ResponseWriter, r *http.Request) {
	var request CloudAccountRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	account, err := s.store.CreateCloudAccount(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.syncAccountAsync(account.ID)
	writeJSON(w, http.StatusCreated, map[string]interface{}{"id": account.ID, "message": "账户已添加，正在后台同步"})
}

func (s *Server) legacyUpdateAccount(w http.ResponseWriter, r *http.Request) {
	id, err := legacyAccountID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var request CloudAccountRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := s.store.UpdateCloudAccount(r.Context(), id, request); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeStoreError(w, err)
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "更新成功"})
}

func (s *Server) legacyDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := legacyAccountID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.DeleteCloudAccount(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "删除成功"})
}

func (s *Server) legacyListInstances(w http.ResponseWriter, r *http.Request) {
	overview, err := s.store.CloudOverview(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, overview.Instances)
}

func (s *Server) legacySyncInstances(w http.ResponseWriter, r *http.Request) {
	results, err := s.cloud.SyncAll(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "同步完成", "results": results})
}

func (s *Server) legacySyncInstance(w http.ResponseWriter, r *http.Request) {
	result, err := s.cloud.SyncInstance(r.Context(), chi.URLParam(r, "instanceID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "同步完成", "result": result})
}

func (s *Server) legacyRenameInstance(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.cloud.RenameInstance(r.Context(), chi.URLParam(r, "instanceID"), request.Name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "更新成功"})
}

func (s *Server) legacyReleaseInstance(w http.ResponseWriter, r *http.Request) {
	if err := s.cloud.ReleaseInstance(r.Context(), chi.URLParam(r, "instanceID")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "实例已释放"})
}

func (s *Server) legacyBilling(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "accountID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid account id"))
		return
	}
	response, err := s.cloud.Billing(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) legacyGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetPublicSettings(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) legacyUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var items []SettingUpdate
	if err := decodeJSON(r, &items); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.UpdateSettings(r.Context(), items); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "保存成功"})
}

func (s *Server) legacyTestTelegram(w http.ResponseWriter, r *http.Request) {
	if err := s.cloud.TestTelegram(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "测试消息已发送"})
}

func (s *Server) legacyTestDailyReport(w http.ResponseWriter, r *http.Request) {
	if err := s.cloud.SendDailyReport(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "流量汇报已发送"})
}

func (s *Server) legacyChangePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.ChangeAdminPassword(r.Context(), request.Password); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "密码已更新，请重新登录"})
}

func (s *Server) legacyVersionCheck(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"current": "0.2.0", "latest": "0.2.0", "has_update": false,
		"url": "https://github.com/R1ddle1337/AliCDT-Manager-Professional/releases",
	})
}

func (s *Server) legacyListLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	logs, err := s.store.ListSystemLogs(r.Context(), r.URL.Query().Get("category"), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) legacyClearLogs(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearSystemLogs(r.Context(), r.URL.Query().Get("category")); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "日志已清空"})
}

func (s *Server) syncAccountAsync(accountID int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if result, err := s.cloud.SyncAccountByID(ctx, accountID); err != nil {
			_ = s.store.AddSystemLog(context.Background(), "warning", "system", "账户后台同步失败: "+err.Error())
		} else if result.Error != "" {
			_ = s.store.AddSystemLog(context.Background(), "warning", "system", "账户后台同步部分失败: "+result.Error)
		}
	}()
}

func legacyAccountID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "accountID"), 10, 64)
	if err != nil {
		return 0, errors.New("invalid account id")
	}
	return id, nil
}
