package main

import (
	"log"
	"net/http"
	"news-aggregator/internal/config"
	"news-aggregator/internal/handlers"
	"news-aggregator/internal/scheduler"
)

func main() {
	cfgMgr := config.NewManager()
	sched := scheduler.NewScheduler(cfgMgr)

	if err := sched.Start(); err != nil {
		log.Printf("Failed to start scheduler: %v", err)
	}

	webHandler, err := handlers.NewWebHandler(cfgMgr, sched)
	if err != nil {
		log.Fatalf("Failed to create web handler: %v", err)
	}

	apiHandler := handlers.NewAPIHandler(cfgMgr, sched)

	http.HandleFunc("/", webHandler.Index)
	http.HandleFunc("/config", webHandler.Config)
	http.HandleFunc("/scheduler", webHandler.Scheduler)

	http.HandleFunc("/api/config/grok", apiHandler.UpdateGrokConfig)
	http.HandleFunc("/api/config/sheets", apiHandler.UpdateSheetsConfig)
	http.HandleFunc("/api/config/topics", apiHandler.UpdateTopics)
	http.HandleFunc("/api/config/schedule", apiHandler.UpdateSchedule)
	http.HandleFunc("/api/execute", apiHandler.ExecuteNow)
	http.HandleFunc("/api/logs", apiHandler.GetLogs)
	http.HandleFunc("/api/status", apiHandler.GetStatus)

	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
