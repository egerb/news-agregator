package scheduler

import (
	"context"
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
	executing          bool
	interruptRequested bool
	execCancel         context.CancelFunc // cancels in-flight execution for immediate interrupt
	currentTopic       string
	totalTopics        int
	processedTopics    int
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
	s.interruptRequested = false
	s.currentTopic = ""
	s.totalTopics = 0
	s.processedTopics = 0
	ctx, cancel := context.WithCancel(context.Background())
	s.execCancel = cancel
	s.mu.Unlock()

	defer func() {
		cancel()
		s.mu.Lock()
		s.execCancel = nil
		s.executing = false
		s.currentTopic = ""
		s.totalTopics = 0
		s.processedTopics = 0
		s.mu.Unlock()
		time.Sleep(2 * time.Second)
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

	grokClient := grok.NewClient(cfg.Grok.APIKey, cfg.Grok.BaseURL, cfg.Grok.Model, cfg.Grok.Tools)
	sheetsClient, err := sheets.NewClient(cfg.Sheets.CredentialsJSON, cfg.Sheets.SpreadsheetID, cfg.Sheets.SheetName)
	if err != nil {
		s.addLog(models.ExecutionLog{
			Timestamp: time.Now(),
			Success:   false,
			Message:   fmt.Sprintf("Failed to create sheets client: %v", err),
		})
		return
	}

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

	var totalWritten int
	interrupted := false
	for i, tp := range validTopics {
		s.mu.Lock()
		if s.interruptRequested {
			s.mu.Unlock()
			log.Printf("[%s] [EXECUTION] Interrupt requested, stopping", time.Now().Format("2006-01-02 15:04:05"))
			interrupted = true
			break
		}
		s.currentTopic = tp.Topic
		s.processedTopics = i
		s.mu.Unlock()

		log.Printf("[%s] [EXECUTION] Processing topic %d/%d: %s (Progress: %d/%d)", time.Now().Format("2006-01-02 15:04:05"), i+1, len(validTopics), tp.Topic, i, len(validTopics))

		validatedLinks, reqURL, reqBody, respCode, respBody, validationErrors, llmDuration, err := grokClient.FetchLinks(ctx, tp.Prompt, tp.Count)
		if ctx.Err() != nil {
			interrupted = true
			log.Printf("[%s] [EXECUTION] Interrupted (context cancelled)", time.Now().Format("2006-01-02 15:04:05"))
			break
		}
		if err != nil {
			log.Printf("[%s] [EXECUTION ERROR] Failed to fetch links for topic '%s': %v", time.Now().Format("2006-01-02 15:04:05"), tp.Topic, err)
			s.addLog(models.ExecutionLog{
				Timestamp:           time.Now(),
				Success:             false,
				Message:             fmt.Sprintf("Failed to fetch links: %v", err),
				Topic:               tp.Topic,
				LinksCount:          0,
				ParsedLinks:         []string{},
				RequestURL:          reqURL,
				RequestBody:         reqBody,
				ResponseCode:        respCode,
				ResponseBody:        respBody,
				ResponseTimeSeconds: llmDuration.Seconds(),
				Error:               err.Error(),
			})
			s.mu.Lock()
			s.processedTopics = i + 1
			s.mu.Unlock()
			continue
		}

		ts := time.Now().Format("2006-01-02 15:04:05")
		for _, link := range validatedLinks {
			log.Printf("[%s] [EXECUTION] Validated link: %s (200)", ts, link)
		}
		for u, reason := range validationErrors {
			log.Printf("[%s] [EXECUTION] Invalid link: %s (%s)", ts, u, reason)
		}

		log.Printf("[%s] [EXECUTION] Topic '%s': %d validated, %d invalid", ts, tp.Topic, len(validatedLinks), len(validationErrors))
		isSuccess := respCode >= 200 && respCode < 300
		logMessage := fmt.Sprintf("Fetched %d validated links", len(validatedLinks))
		if len(validationErrors) > 0 {
			logMessage = fmt.Sprintf("%s, %d invalid", logMessage, len(validationErrors))
		}
		if !isSuccess {
			logMessage = fmt.Sprintf("Request failed with status %d: %s", respCode, respBody)
		}
		s.addLog(models.ExecutionLog{
			Timestamp:           time.Now(),
			Success:             isSuccess,
			Message:             logMessage,
			Topic:               tp.Topic,
			LinksCount:          len(validatedLinks),
			ParsedLinks:         validatedLinks,
			ValidationErrors:    validationErrors,
			RequestURL:          reqURL,
			RequestBody:         reqBody,
			ResponseCode:        respCode,
			ResponseBody:        respBody,
			ResponseTimeSeconds: llmDuration.Seconds(),
		})

		topicItems := make([]models.NewsItem, 0, len(validatedLinks))
		for _, link := range validatedLinks {
			topicItems = append(topicItems, models.NewsItem{URL: link, Topic: tp.Topic, Priority: 1})
		}
		if len(topicItems) > 0 {
			if i == 0 {
				log.Printf("[%s] [EXECUTION] Clearing data rows from Google Sheet", ts)
				if err := sheetsClient.ClearDataRows(); err != nil {
					log.Printf("[%s] [EXECUTION ERROR] Failed to clear data rows: %v", ts, err)
					s.addLog(models.ExecutionLog{
						Timestamp: time.Now(),
						Success:   false,
						Message:   fmt.Sprintf("Failed to clear data rows: %v", err),
					})
					s.mu.Lock()
					s.processedTopics = i + 1
					s.mu.Unlock()
					return
				}
				log.Printf("[%s] [EXECUTION] Data rows cleared successfully", ts)
				if err := sheetsClient.WriteNewsItems(topicItems); err != nil {
					log.Printf("[%s] [EXECUTION ERROR] Failed to write to sheet: %v", ts, err)
					s.addLog(models.ExecutionLog{
						Timestamp: time.Now(),
						Success:   false,
						Message:   fmt.Sprintf("Failed to write to sheet: %v", err),
					})
					s.mu.Lock()
					s.processedTopics = i + 1
					s.mu.Unlock()
					return
				}
				log.Printf("[%s] [EXECUTION] Wrote %d items to sheet (topic %s)", ts, len(topicItems), tp.Topic)
			} else {
				if err := sheetsClient.AppendNewsItems(topicItems); err != nil {
					log.Printf("[%s] [EXECUTION ERROR] Failed to append to sheet: %v", ts, err)
					s.addLog(models.ExecutionLog{
						Timestamp: time.Now(),
						Success:   false,
						Message:   fmt.Sprintf("Failed to append to sheet: %v", err),
					})
					s.mu.Lock()
					s.processedTopics = i + 1
					s.mu.Unlock()
					return
				}
				log.Printf("[%s] [EXECUTION] Appended %d items to sheet (topic %s)", ts, len(topicItems), tp.Topic)
			}
			totalWritten += len(topicItems)
		}

		s.mu.Lock()
		s.processedTopics = i + 1
		s.mu.Unlock()
	}

	log.Printf("[%s] [EXECUTION] Loop finished, processed %d topics", time.Now().Format("2006-01-02 15:04:05"), len(validTopics))

	s.mu.Lock()
	s.processedTopics = len(validTopics)
	s.currentTopic = ""
	s.mu.Unlock()

	if interrupted {
		log.Printf("[%s] [EXECUTION] Execution interrupted by user", time.Now().Format("2006-01-02 15:04:05"))
		s.addLog(models.ExecutionLog{
			Timestamp: time.Now(),
			Success:   false,
			Message:   fmt.Sprintf("Execution interrupted (wrote %d items)", totalWritten),
		})
		return
	}

	log.Printf("[%s] [EXECUTION] Completed processing all topics (%d/%d)", time.Now().Format("2006-01-02 15:04:05"), len(validTopics), len(validTopics))

	s.addLog(models.ExecutionLog{
		Timestamp: time.Now(),
		Success:   true,
		Message:   fmt.Sprintf("Successfully wrote %d items to sheet", totalWritten),
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

func (s *Scheduler) RequestInterrupt() {
	s.mu.Lock()
	cancel := s.execCancel
	s.execCancel = nil
	s.interruptRequested = true
	s.mu.Unlock()
	if cancel != nil {
		cancel() // abort in-flight LLM request immediately
	}
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
