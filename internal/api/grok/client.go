package grok

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	APIKey     string
	BaseURL    string
	Model      string
	Tools      []string
	httpClient *http.Client
}

func NewClient(apiKey, baseURL, model string, tools []string) *Client {
	return &Client{
		APIKey:     apiKey,
		BaseURL:    strings.TrimSpace(baseURL),
		Model:      model,
		Tools:      tools,
		httpClient: &http.Client{
			Timeout: 300 * time.Second,
		},
	}
}

func (c *Client) FetchLinks(prompt string, desiredCount int) ([]string, string, string, int, string, error) {
	reqURL := strings.TrimSuffix(strings.TrimSpace(c.BaseURL), "/")

	tools := make([]map[string]interface{}, 0)
	
	now := time.Now()
	fromDate := now.AddDate(0, 0, -1).Format("2006-01-02")
	toDate := now.Format("2006-01-02")
	
	hasWebSearch := false
	hasXSearch := false
	
	for _, toolType := range c.Tools {
		toolType = strings.TrimSpace(toolType)
		if toolType == "" {
			continue
		}
		
		if toolType == "web_search" && !hasWebSearch {
			tools = append(tools, map[string]interface{}{
				"type": "web_search",
				"parameters": map[string]interface{}{
					"enable_image_understanding": false,
				},
			})
			hasWebSearch = true
		} else if toolType == "x_search" && !hasXSearch {
			tools = append(tools, map[string]interface{}{
				"type": "x_search",
				"parameters": map[string]interface{}{
					"from_date":                fromDate,
					"to_date":                  toDate,
					"enable_image_understanding": false,
					"enable_video_understanding": false,
				},
			})
			hasXSearch = true
		} else if toolType != "web_search" && toolType != "x_search" && toolType != "live_search" {
			tools = append(tools, map[string]interface{}{
				"type": toolType,
				"parameters": map[string]interface{}{},
			})
		}
	}
	
	if !hasWebSearch && !hasXSearch && len(tools) == 0 {
		tools = append(tools,
			map[string]interface{}{
				"type": "web_search",
				"parameters": map[string]interface{}{
					"enable_image_understanding": false,
				},
			},
			map[string]interface{}{
				"type": "x_search",
				"parameters": map[string]interface{}{
					"from_date":                fromDate,
					"to_date":                  toDate,
					"enable_image_understanding": false,
					"enable_video_understanding": false,
				},
			},
		)
	}

	systemText := "You are a news aggregator. You MUST use web_search and x_search tools to find recent news articles from the last 24 hours. Keep searching until you can return the full requested number of URLs. Return ONLY the URLs, one per line. Do not include any other text, explanations, or formatting. Just return the URLs."
	countNote := ""
	if desiredCount > 0 {
		countNote = fmt.Sprintf(" Target links: %d.", desiredCount)
	}
	userText := fmt.Sprintf("Today is %s. %s\n\nIMPORTANT: You MUST use the search tools (web_search and x_search) to find recent news. Search for news from the last 24 hours only (from %s to %s). Keep searching until you can return the full requested number of URLs.%s After searching, return ONLY URLs, one per line. No explanations, no formatting, no other text. Just the URLs.", toDate, prompt, fromDate, toDate, countNote)

	reqBody := map[string]interface{}{
		"model":       c.Model,
		"input": []map[string]interface{}{
			{
				"role": "system",
				"content": []map[string]interface{}{
					{"type": "input_text", "text": systemText},
				},
			},
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "input_text", "text": userText},
				},
			},
		},
		"temperature": 0.7,
		"reasoning": map[string]string{
			"effort": "high",
		},
		"max_turns": 8,
	}

	if len(tools) > 0 {
		reqBody["tools"] = tools
	}

	reqBodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, reqURL, "", 0, "", err
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("[%s] [GROK REQUEST] URL: %s", timestamp, reqURL)
	log.Printf("[%s] [GROK REQUEST] Body: %s", timestamp, string(reqBodyJSON))

	httpReq, err := http.NewRequest("POST", reqURL, bytes.NewBuffer(reqBodyJSON))
	if err != nil {
		return nil, reqURL, string(reqBodyJSON), 0, "", err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.APIKey))

	startTime := time.Now()
	httpResp, err := c.httpClient.Do(httpReq)
	duration := time.Since(startTime)

	if err != nil {
		log.Printf("[%s] [GROK ERROR] Request failed after %v: %v", time.Now().Format("2006-01-02 15:04:05"), duration, err)
		return nil, reqURL, string(reqBodyJSON), 0, err.Error(), err
	}
	defer httpResp.Body.Close()

	bodyBytes, _ := io.ReadAll(httpResp.Body)
	responseBody := string(bodyBytes)
	respCode := httpResp.StatusCode

	log.Printf("[%s] [GROK RESPONSE] Status: %d, Duration: %v", time.Now().Format("2006-01-02 15:04:05"), respCode, duration)
	log.Printf("[%s] [GROK RESPONSE] Body: %s", time.Now().Format("2006-01-02 15:04:05"), truncateString(responseBody, 1000))

	if respCode != http.StatusOK {
		return nil, reqURL, string(reqBodyJSON), respCode, responseBody, fmt.Errorf("grok API error: %d - %s", respCode, responseBody)
	}

	var respEnvelope struct {
		OutputText string `json:"output_text"`
		Citations  []string `json:"citations"`
		Output     []struct {
			Type    string `json:"type"`
			Action  struct {
				Sources []struct {
					URL string `json:"url"`
				} `json:"sources"`
			} `json:"action"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}

	if err := json.Unmarshal(bodyBytes, &respEnvelope); err != nil {
		return nil, reqURL, string(reqBodyJSON), respCode, responseBody, err
	}

	finalContent := strings.TrimSpace(respEnvelope.OutputText)
	if finalContent == "" {
		for _, out := range respEnvelope.Output {
			if out.Type != "message" {
				continue
			}
			for _, c := range out.Content {
				if c.Type == "output_text" && strings.TrimSpace(c.Text) != "" {
					finalContent = strings.TrimSpace(c.Text)
					break
				}
			}
			if finalContent != "" {
				break
			}
		}
	}

	links := make([]string, 0)
	if finalContent != "" {
		log.Printf("[%s] [GROK PARSING] Response content length: %d", time.Now().Format("2006-01-02 15:04:05"), len(finalContent))
		log.Printf("[%s] [GROK PARSING] Response content preview: %s", time.Now().Format("2006-01-02 15:04:05"), truncateString(finalContent, 500))
		links = append(links, extractURLs(finalContent)...)
	}

	if len(respEnvelope.Citations) > 0 {
		links = append(links, respEnvelope.Citations...)
	}

	for _, out := range respEnvelope.Output {
		if len(out.Action.Sources) == 0 {
			continue
		}
		for _, src := range out.Action.Sources {
			if strings.TrimSpace(src.URL) != "" {
				links = append(links, strings.TrimSpace(src.URL))
			}
		}
	}

	if len(links) == 0 {
		return nil, reqURL, string(reqBodyJSON), respCode, responseBody, fmt.Errorf("no links found in response output")
	}

	links = dedupeLinks(links)
	if desiredCount > 0 && len(links) > desiredCount {
		links = links[:desiredCount]
	}

	log.Printf("[%s] [GROK PARSING] Extracted %d links (requested: %d): %v", time.Now().Format("2006-01-02 15:04:05"), len(links), desiredCount, links)

	return links, reqURL, string(reqBodyJSON), respCode, responseBody, nil
}

func dedupeLinks(links []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(links))
	for _, link := range links {
		link = strings.TrimSpace(link)
		if link == "" || seen[link] {
			continue
		}
		seen[link] = true
		out = append(out, link)
	}
	return out
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func extractURLs(text string) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`https?://[^\s<>"{}|\\^` + "`" + `\[\]()]+`),
		regexp.MustCompile(`https?://[^\s<>"]+`),
		regexp.MustCompile(`https?://[^\s]+`),
	}

	seen := make(map[string]bool)
	var allMatches []string

	for _, pattern := range patterns {
		matches := pattern.FindAllString(text, -1)
		for _, match := range matches {
			match = strings.TrimSpace(match)
			match = strings.TrimRight(match, ".,;:!?)")
			match = strings.TrimRight(match, ".,;:!?)")
			if match != "" && !seen[match] {
				seen[match] = true
				allMatches = append(allMatches, match)
			}
		}
		if len(allMatches) > 0 {
			break
		}
	}

	return allMatches
}
