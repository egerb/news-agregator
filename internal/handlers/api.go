package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
	"news-aggregator/internal/config"
	"news-aggregator/internal/models"
	"news-aggregator/internal/scheduler"
)

type APIHandler struct {
	cfgMgr    *config.Manager
	scheduler *scheduler.Scheduler
}

func NewAPIHandler(cfgMgr *config.Manager, sched *scheduler.Scheduler) *APIHandler {
	return &APIHandler{
		cfgMgr:    cfgMgr,
		scheduler: sched,
	}
}

func (h *APIHandler) UpdateGrokConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var grokCfg models.GrokConfig
	if err := json.NewDecoder(r.Body).Decode(&grokCfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := h.cfgMgr.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cfg.Grok = grokCfg

	if err := h.cfgMgr.Save(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *APIHandler) UpdateSheetsConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var sheetsCfg models.SheetsConfig
	if err := json.NewDecoder(r.Body).Decode(&sheetsCfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := h.cfgMgr.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cfg.Sheets = sheetsCfg

	if err := h.cfgMgr.Save(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *APIHandler) UpdateTopics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var topics []models.TopicPrompt
	if err := json.NewDecoder(r.Body).Decode(&topics); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := h.cfgMgr.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cfg.Topics = topics

	if err := h.cfgMgr.Save(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *APIHandler) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var schedule models.ScheduleConfig
	if err := json.NewDecoder(r.Body).Decode(&schedule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := h.cfgMgr.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cfg.Schedule = schedule

	if err := h.cfgMgr.Save(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if schedule.Enabled {
		if err := h.scheduler.Restart(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		h.scheduler.Stop()
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *APIHandler) ExecuteNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.scheduler.ExecuteNow(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *APIHandler) Interrupt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.scheduler.RequestInterrupt()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *APIHandler) GetLogs(w http.ResponseWriter, r *http.Request) {
	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")

	page := 1
	limit := 10

	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	logs, total := h.scheduler.GetLogsPaginated(page, limit)

	response := map[string]interface{}{
		"logs":       logs,
		"page":       page,
		"limit":      limit,
		"total":      total,
		"totalPages": (total + limit - 1) / limit,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *APIHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	isExecuting, currentTopic, processed, total := h.scheduler.GetProgress()
	nextRun := h.scheduler.GetNextRunTime()
	
	progressPercent := 0
	if total > 0 {
		progressPercent = (processed * 100) / total
		if progressPercent > 100 {
			progressPercent = 100
		}
	}
	
	status := map[string]interface{}{
		"is_running":       h.scheduler.IsRunning(),
		"is_executing":     isExecuting,
		"current_topic":    currentTopic,
		"processed":        processed,
		"total":            total,
		"progress_percent": progressPercent,
	}
	
	if nextRun != nil {
		status["next_run_time"] = nextRun.Format(time.RFC3339)
	} else {
		status["next_run_time"] = nil
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
