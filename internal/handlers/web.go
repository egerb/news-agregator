package handlers

import (
	"html/template"
	"net/http"
	"news-aggregator/internal/config"
	"news-aggregator/internal/scheduler"
)

type WebHandler struct {
	cfgMgr    *config.Manager
	scheduler *scheduler.Scheduler
	templates *template.Template
}

func NewWebHandler(cfgMgr *config.Manager, sched *scheduler.Scheduler) (*WebHandler, error) {
	tmpl, err := template.ParseGlob("web/templates/*.html")
	if err != nil {
		return nil, err
	}

	return &WebHandler{
		cfgMgr:    cfgMgr,
		scheduler: sched,
		templates: tmpl,
	}, nil
}

func (h *WebHandler) Index(w http.ResponseWriter, r *http.Request) {
	cfg, _ := h.cfgMgr.Load()
	logs, total := h.scheduler.GetLogsPaginated(1, 10)
	nextRun := h.scheduler.GetNextRunTime()

	data := map[string]interface{}{
		"Config":      cfg,
		"Logs":        logs,
		"TotalLogs":   total,
		"IsRunning":   h.scheduler.IsRunning(),
		"IsExecuting": h.scheduler.IsExecuting(),
		"NextRunTime": nextRun,
	}

	if err := h.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *WebHandler) Config(w http.ResponseWriter, r *http.Request) {
	cfg, _ := h.cfgMgr.Load()
	data := map[string]interface{}{
		"Config": cfg,
	}
	if err := h.templates.ExecuteTemplate(w, "config.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *WebHandler) Scheduler(w http.ResponseWriter, r *http.Request) {
	cfg, _ := h.cfgMgr.Load()
	data := map[string]interface{}{
		"Config":    cfg,
		"IsRunning": h.scheduler.IsRunning(),
	}
	if err := h.templates.ExecuteTemplate(w, "scheduler.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
