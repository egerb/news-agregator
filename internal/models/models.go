package models

import "time"

type GrokConfig struct {
	APIKey  string   `json:"api_key"`
	BaseURL string   `json:"base_url"`
	Model   string   `json:"model"`
	Tools   []string `json:"tools"`
}

type SheetsConfig struct {
	CredentialsJSON string `json:"credentials_json"`
	SpreadsheetID   string `json:"spreadsheet_id"`
	SheetName       string `json:"sheet_name"`
}

type TopicPrompt struct {
	Topic  string `json:"topic"`
	Prompt string `json:"prompt"`
	Count  int    `json:"count"`
}

type NewsItem struct {
	URL      string `json:"url"`
	Topic    string `json:"topic"`
	Priority int    `json:"priority"`
}

type ScheduleConfig struct {
	Enabled   bool   `json:"enabled"`
	Type      string `json:"type"`
	Time      string `json:"time"`
	DayOfWeek int    `json:"day_of_week"`
}

type Config struct {
	Grok      GrokConfig      `json:"grok"`
	Sheets    SheetsConfig    `json:"sheets"`
	Topics    []TopicPrompt   `json:"topics"`
	Schedule  ScheduleConfig  `json:"schedule"`
}

type ExecutionLog struct {
	Timestamp    time.Time `json:"timestamp"`
	Success      bool      `json:"success"`
	Message      string    `json:"message"`
	Topic        string    `json:"topic,omitempty"`
	LinksCount   int       `json:"links_count,omitempty"`
	ParsedLinks  []string  `json:"parsed_links,omitempty"`
	RequestURL   string    `json:"request_url,omitempty"`
	RequestBody  string    `json:"request_body,omitempty"`
	ResponseCode int       `json:"response_code,omitempty"`
	ResponseBody string    `json:"response_body,omitempty"`
	Error        string    `json:"error,omitempty"`
}
