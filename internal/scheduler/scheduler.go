package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"news-aggregator/internal/api/grok"
	"news-aggregator/internal/api/sheets"
	"news-aggregator/internal/config"
	"news-aggregator/internal/models"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

type Scheduler struct {
	cron           *cron.Cron
	cronEntryID    cron.EntryID
	cfgMgr         *config.Manager
	running        bool
	mu             sync.Mutex
	logs           []models.ExecutionLog
	maxLogs        int
	executing      bool
	currentTopic   string
	totalTopics    int
	processedTopics int
}

const logsFile = "logs.json"

func getLogsPath() string {
	wd, err := os.Getwd()
	if err != nil {
		return logsFile
	}
	return wd + "/" + logsFile
}

func NewScheduler(cfgMgr *config.Manager) *Scheduler {
	s := &Scheduler{
		cron:    nil,
		cfgMgr:  cfgMgr,
		logs:    make([]models.ExecutionLog, 0),
		maxLogs: 1000,
	}
	s.loadLogs()
	return s
}

func (s *Scheduler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("scheduler already running")
	}

	cfg, err := s.cfgMgr.Load()
	if err != nil {
		return err
	}

	if !cfg.Schedule.Enabled {
		return nil
	}

	s.cron = cron.New()

	spec, err := s.buildCronSpec(cfg.Schedule)
	if err != nil {
		return err
	}

	entryID, err := s.cron.AddFunc(spec, func() {
		s.execute()
	})
	if err != nil {
		return err
	}
	s.cronEntryID = entryID

	s.cron.Start()
	s.running = true

	return nil
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		s.cron.Stop()
		s.running = false
	}
}

func (s *Scheduler) ExecuteNow() error {
	if s.executing {
		return fmt.Errorf("execution already in progress")
	}

	cfg, err := s.cfgMgr.Load()
	if err != nil {
		s.addLog(models.ExecutionLog{
			Timestamp: time.Now(),
			Success:   false,
			Message:   fmt.Sprintf("Failed to load config: %v", err),
		})
		return err
	}

	if strings.TrimSpace(cfg.Grok.BaseURL) == "" {
		err := fmt.Errorf("Grok Base URL not configured")
		s.addLog(models.ExecutionLog{
			Timestamp: time.Now(),
			Success:   false,
			Message:   err.Error(),
		})
		return err
	}

	go s.execute()
	return nil
}

func (s *Scheduler) execute() {
	s.mu.Lock()
	if s.executing {
		s.mu.Unlock()
		return
	}
	s.executing = true
	s.currentTopic = ""
	s.totalTopics = 0
	s.processedTopics = 0
	s.mu.Unlock()

	defer func() {
		time.Sleep(2 * time.Second)
		s.mu.Lock()
		s.executing = false
		s.currentTopic = ""
		s.totalTopics = 0
		s.processedTopics = 0
		s.mu.Unlock()
		log.Printf("[%s] [EXECUTION] Execution state reset", time.Now().Format("2006-01-02 15:04:05"))
	}()

	log.Printf("[%s] [EXECUTION] Loading configuration", time.Now().Format("2006-01-02 15:04:05"))
	cfg, err := s.cfgMgr.Load()
	if err != nil {
		log.Printf("[%s] [EXECUTION ERROR] Failed to load config: %v", time.Now().Format("2006-01-02 15:04:05"), err)
		s.addLog(models.ExecutionLog{
			Timestamp: time.Now(),
			Success:   false,
			Message:   fmt.Sprintf("Failed to load config: %v", err),
		})
		return
	}

	if cfg.Grok.APIKey == "" {
		log.Printf("[%s] [EXECUTION ERROR] Grok API key not configured", time.Now().Format("2006-01-02 15:04:05"))
		s.addLog(models.ExecutionLog{
			Timestamp: time.Now(),
			Success:   false,
			Message:   "Grok API key not configured",
		})
		return
	}
	if strings.TrimSpace(cfg.Grok.BaseURL) == "" {
		log.Printf("[%s] [EXECUTION ERROR] Grok Base URL not configured", time.Now().Format("2006-01-02 15:04:05"))
		s.addLog(models.ExecutionLog{
			Timestamp: time.Now(),
			Success:   false,
			Message:   "Grok Base URL not configured",
		})
		return
	}

	if cfg.Sheets.CredentialsJSON == "" || cfg.Sheets.SpreadsheetID == "" {
		log.Printf("[%s] [EXECUTION ERROR] Google Sheets not configured", time.Now().Format("2006-01-02 15:04:05"))
		s.addLog(models.ExecutionLog{
			Timestamp: time.Now(),
			Success:   false,
			Message:   "Google Sheets not configured",
		})
		return
	}
	
	log.Printf("[%s] [EXECUTION] Creating API clients", time.Now().Format("2006-01-02 15:04:05"))

	grokClient := grok.NewClient(cfg.Grok.APIKey, "", cfg.Grok.Model, cfg.Grok.Tools)
	sheetsClient, err := sheets.NewClient(cfg.Sheets.CredentialsJSON, cfg.Sheets.SpreadsheetID, cfg.Sheets.SheetName)
	if err != nil {
		s.addLog(models.ExecutionLog{
			Timestamp: time.Now(),
			Success:   false,
			Message:   fmt.Sprintf("Failed to create sheets client: %v", err),
		})
		return
	}

	log.Printf("[%s] [EXECUTION] Clearing data rows from Google Sheet", time.Now().Format("2006-01-02 15:04:05"))
	if err := sheetsClient.ClearDataRows(); err != nil {
		log.Printf("[%s] [EXECUTION ERROR] Failed to clear data rows: %v", time.Now().Format("2006-01-02 15:04:05"), err)
		s.addLog(models.ExecutionLog{
			Timestamp: time.Now(),
			Success:   false,
			Message:   fmt.Sprintf("Failed to clear data rows: %v", err),
		})
		return
	}
	log.Printf("[%s] [EXECUTION] Data rows cleared successfully", time.Now().Format("2006-01-02 15:04:05"))

	var allItems []models.NewsItem

	validTopics := make([]models.TopicPrompt, 0)
	for _, tp := range cfg.Topics {
		if tp.Topic != "" && tp.Prompt != "" {
			validTopics = append(validTopics, tp)
		}
	}

	s.mu.Lock()
	s.totalTopics = len(validTopics)
	s.mu.Unlock()

	log.Printf("[%s] [EXECUTION] Starting execution with %d topics", time.Now().Format("2006-01-02 15:04:05"), len(validTopics))
	for idx, tp := range validTopics {
		log.Printf("[%s] [EXECUTION] Loop iteration %d: topic='%s', prompt length=%d", time.Now().Format("2006-01-02 15:04:05"), idx, tp.Topic, len(tp.Prompt))
	}

	for i, tp := range validTopics {
		s.mu.Lock()
		s.currentTopic = tp.Topic
		s.processedTopics = i
		s.mu.Unlock()

		log.Printf("[%s] [EXECUTION] Processing topic %d/%d: %s (Progress: %d/%d)", time.Now().Format("2006-01-02 15:04:05"), i+1, len(validTopics), tp.Topic, i, len(validTopics))

		links, reqURL, reqBody, respCode, respBody, err := grokClient.FetchLinks(tp.Prompt, tp.Count)
		if err != nil {
			log.Printf("[%s] [EXECUTION ERROR] Failed to fetch links for topic '%s': %v", time.Now().Format("2006-01-02 15:04:05"), tp.Topic, err)
			s.addLog(models.ExecutionLog{
				Timestamp:    time.Now(),
				Success:      false,
				Message:      fmt.Sprintf("Failed to fetch links: %v", err),
				Topic:        tp.Topic,
				LinksCount:   0,
				ParsedLinks:  []string{},
				RequestURL:   reqURL,
				RequestBody:  reqBody,
				ResponseCode: respCode,
				ResponseBody: respBody,
				Error:        err.Error(),
			})
			s.mu.Lock()
			s.processedTopics = i + 1
			s.mu.Unlock()
			continue
		}

		validLinks := make([]string, 0)
		for _, link := range links {
			trimmedLink := strings.TrimSpace(link)
			trimmedLink = strings.TrimRight(trimmedLink, ".,;:!?)")
			trimmedLink = strings.TrimRight(trimmedLink, ".,;:!?)")
			
			if trimmedLink != "" && len(trimmedLink) > 10 && strings.HasPrefix(trimmedLink, "http") {
				validLinks = append(validLinks, trimmedLink)
				allItems = append(allItems, models.NewsItem{
					URL:      trimmedLink,
					Topic:    tp.Topic,
					Priority: 1,
				})
				log.Printf("[%s] [EXECUTION] Added valid link: %s", time.Now().Format("2006-01-02 15:04:05"), trimmedLink)
			} else {
				log.Printf("[%s] [EXECUTION] Rejected invalid link (len=%d, prefix=%s): %s", time.Now().Format("2006-01-02 15:04:05"), len(trimmedLink), func() string {
					if len(trimmedLink) > 5 {
						return trimmedLink[:5]
					}
					return trimmedLink
				}(), trimmedLink)
			}
		}

		log.Printf("[%s] [EXECUTION] Successfully parsed %d valid links from %d total links for topic '%s'", time.Now().Format("2006-01-02 15:04:05"), len(validLinks), len(links), tp.Topic)
		if len(validLinks) > 0 {
			log.Printf("[%s] [EXECUTION] Valid links: %v", time.Now().Format("2006-01-02 15:04:05"), validLinks)
		} else {
			log.Printf("[%s] [EXECUTION] WARNING: No valid links found! All %d links were rejected", time.Now().Format("2006-01-02 15:04:05"), len(links))
		}
		
		isSuccess := respCode >= 200 && respCode < 300
		logMessage := fmt.Sprintf("Fetched %d valid links (from %d total)", len(validLinks), len(links))
		if !isSuccess {
			logMessage = fmt.Sprintf("Request failed with status %d: %s", respCode, respBody)
		}
		
		log.Printf("[%s] [EXECUTION] About to add log entry for topic '%s'", time.Now().Format("2006-01-02 15:04:05"), tp.Topic)
		s.addLog(models.ExecutionLog{
			Timestamp:    time.Now(),
			Success:      isSuccess,
			Message:      logMessage,
			Topic:        tp.Topic,
			LinksCount:   len(validLinks),
			ParsedLinks:  validLinks,
			RequestURL:   reqURL,
			RequestBody:  reqBody,
			ResponseCode: respCode,
			ResponseBody: respBody,
		})
		log.Printf("[%s] [EXECUTION] Log entry added for topic '%s', continuing loop...", time.Now().Format("2006-01-02 15:04:05"), tp.Topic)

		s.mu.Lock()
		s.processedTopics = i + 1
		s.mu.Unlock()
		log.Printf("[%s] [EXECUTION] Completed topic %d/%d: %s, moving to next topic...", time.Now().Format("2006-01-02 15:04:05"), i+1, len(validTopics), tp.Topic)
		log.Printf("[%s] [EXECUTION] Loop will continue, i=%d, len(validTopics)=%d, will continue=%v", time.Now().Format("2006-01-02 15:04:05"), i, len(validTopics), i+1 < len(validTopics))
	}

	log.Printf("[%s] [EXECUTION] Loop finished, processed %d topics", time.Now().Format("2006-01-02 15:04:05"), len(validTopics))

	s.mu.Lock()
	s.processedTopics = len(validTopics)
	s.currentTopic = ""
	s.mu.Unlock()

	log.Printf("[%s] [EXECUTION] Completed processing all topics (%d/%d)", time.Now().Format("2006-01-02 15:04:05"), len(validTopics), len(validTopics))
	
	time.Sleep(1 * time.Second)

	log.Printf("[%s] [EXECUTION] Total items to write: %d", time.Now().Format("2006-01-02 15:04:05"), len(allItems))
	if len(allItems) > 0 {
		for i, item := range allItems {
			log.Printf("[%s] [EXECUTION] Item %d: URL=%s, Topic=%s, Priority=%d", time.Now().Format("2006-01-02 15:04:05"), i+1, item.URL, item.Topic, item.Priority)
		}
		log.Printf("[%s] [EXECUTION] Writing %d items to Google Sheets", time.Now().Format("2006-01-02 15:04:05"), len(allItems))
		if err := sheetsClient.WriteNewsItems(allItems); err != nil {
			log.Printf("[%s] [EXECUTION ERROR] Failed to write to sheet: %v", time.Now().Format("2006-01-02 15:04:05"), err)
			s.addLog(models.ExecutionLog{
				Timestamp: time.Now(),
				Success:   false,
				Message:   fmt.Sprintf("Failed to write to sheet: %v", err),
			})
			return
		}
		log.Printf("[%s] [EXECUTION] Successfully wrote %d items to Google Sheets", time.Now().Format("2006-01-02 15:04:05"), len(allItems))
	} else {
		log.Printf("[%s] [EXECUTION] WARNING: No items to write to sheet!", time.Now().Format("2006-01-02 15:04:05"))
	}

	s.addLog(models.ExecutionLog{
		Timestamp: time.Now(),
		Success:   true,
		Message:   fmt.Sprintf("Successfully wrote %d items to sheet", len(allItems)),
	})
	
	log.Printf("[%s] [EXECUTION] Execution completed successfully", time.Now().Format("2006-01-02 15:04:05"))
}

func (s *Scheduler) buildCronSpec(schedule models.ScheduleConfig) (string, error) {
	timeStr := schedule.Time
	if len(timeStr) != 5 || timeStr[2] != ':' {
		return "", fmt.Errorf("invalid time format, expected HH:MM")
	}
	
	hourStr := timeStr[:2]
	minuteStr := timeStr[3:]
	
	var hour, minute int
	if _, err := fmt.Sscanf(hourStr, "%d", &hour); err != nil {
		return "", fmt.Errorf("invalid hour: %w", err)
	}
	if _, err := fmt.Sscanf(minuteStr, "%d", &minute); err != nil {
		return "", fmt.Errorf("invalid minute: %w", err)
	}
	
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return "", fmt.Errorf("invalid time: %02d:%02d", hour, minute)
	}

	if schedule.Type == "daily" {
		return fmt.Sprintf("%d %d * * *", minute, hour), nil
	} else if schedule.Type == "weekly" {
		dayOfWeek := schedule.DayOfWeek
		if dayOfWeek < 0 || dayOfWeek > 6 {
			return "", fmt.Errorf("invalid day of week: %d (0=Sunday, 6=Saturday)", dayOfWeek)
		}
		return fmt.Sprintf("%d %d * * %d", minute, hour, dayOfWeek), nil
	}

	return "", fmt.Errorf("invalid schedule type: %s", schedule.Type)
}

func (s *Scheduler) addLog(execLog models.ExecutionLog) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logs = append(s.logs, execLog)
	if len(s.logs) > s.maxLogs {
		s.logs = s.logs[1:]
	}
	
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Panic in saveLogs: %v", r)
			}
		}()
		s.saveLogs()
	}()
}

func (s *Scheduler) loadLogs() {
	logPath := getLogsPath()
	data, err := os.ReadFile(logPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Failed to load logs from %s: %v", logPath, err)
		} else {
			log.Printf("Logs file does not exist yet: %s", logPath)
		}
		return
	}

	var logs []models.ExecutionLog
	if err := json.Unmarshal(data, &logs); err != nil {
		log.Printf("Failed to parse logs from %s: %v", logPath, err)
		return
	}

	s.mu.Lock()
	s.logs = logs
	if len(s.logs) > s.maxLogs {
		s.logs = s.logs[len(s.logs)-s.maxLogs:]
	}
	s.mu.Unlock()
	
	log.Printf("Loaded %d logs from %s", len(logs), logPath)
}

func (s *Scheduler) saveLogs() {
	s.mu.Lock()
	logs := make([]models.ExecutionLog, len(s.logs))
	copy(logs, s.logs)
	s.mu.Unlock()

	logPath := getLogsPath()

	if len(logs) == 0 {
		log.Printf("No logs to save")
		return
	}

	data, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal logs: %v", err)
		return
	}

	if err := os.WriteFile(logPath, data, 0644); err != nil {
		log.Printf("Failed to save logs to %s: %v", logPath, err)
	} else {
		log.Printf("Saved %d logs to %s", len(logs), logPath)
	}
}

func (s *Scheduler) GetLogs() []models.ExecutionLog {
	s.mu.Lock()
	defer s.mu.Unlock()

	logs := make([]models.ExecutionLog, len(s.logs))
	copy(logs, s.logs)
	return logs
}

func (s *Scheduler) GetLogsPaginated(page, limit int) ([]models.ExecutionLog, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := len(s.logs)
	if total == 0 {
		return []models.ExecutionLog{}, 0
	}

	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 10
	}

	reversedLogs := make([]models.ExecutionLog, total)
	for i := 0; i < total; i++ {
		reversedLogs[i] = s.logs[total-1-i]
	}

	start := (page - 1) * limit
	end := start + limit

	if start >= total {
		return []models.ExecutionLog{}, total
	}

	if end > total {
		end = total
	}

	logs := make([]models.ExecutionLog, end-start)
	copy(logs, reversedLogs[start:end])

	return logs, total
}

func (s *Scheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Scheduler) IsExecuting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executing
}

func (s *Scheduler) GetProgress() (bool, string, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executing, s.currentTopic, s.processedTopics, s.totalTopics
}

func (s *Scheduler) GetNextRunTime() *time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.cron == nil {
		return nil
	}

	entries := s.cron.Entries()
	for _, entry := range entries {
		if entry.ID == s.cronEntryID {
			nextRun := entry.Next
			return &nextRun
		}
	}

	return nil
}

func (s *Scheduler) Restart() error {
	s.Stop()
	time.Sleep(100 * time.Millisecond)
	return s.Start()
}
