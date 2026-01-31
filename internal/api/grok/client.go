package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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
			Timeout: 15 * time.Minute,
		},
	}
}

func (c *Client) FetchLinks(ctx context.Context, prompt string, desiredCount int) (validated []string, reqURL, reqBody string, respCode int, respBody string, invalidReasons map[string]string, llmDuration time.Duration, err error) {
	invalidReasons = make(map[string]string)
	validatedSet := make(map[string]bool)
	var lastReqURL, lastReqBody string
	var lastRespCode int
	var lastRespBody string
	var lastDuration time.Duration
	var lastCallErr error
	const maxRetries = 5
	validationClient := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		remaining := desiredCount
		if desiredCount > 0 && len(validatedSet) < desiredCount {
			remaining = desiredCount - len(validatedSet)
		}
		excludeURLs := make([]string, 0, len(validatedSet))
		for u := range validatedSet {
			excludeURLs = append(excludeURLs, u)
		}
		rawLinks, rURL, rBody, rCode, rResp, dur, callErr := c.callLLM(ctx, prompt, remaining, excludeURLs)
		lastReqURL, lastReqBody, lastRespCode, lastRespBody, lastDuration = rURL, rBody, rCode, rResp, dur
		if ctx.Err() != nil {
			return nil, lastReqURL, lastReqBody, lastRespCode, lastRespBody, invalidReasons, lastDuration, ctx.Err()
		}
		if callErr != nil {
			lastCallErr = callErr
			log.Printf("[%s] [FETCH LINKS] Attempt %d/%d: LLM call failed: %v", time.Now().Format("2006-01-02 15:04:05"), attempt+1, maxRetries, callErr)
			continue
		}
		valid, invReasons := c.validateLinks(rawLinks, validationClient)
		for u, reason := range invReasons {
			invalidReasons[u] = reason
		}
		ts := time.Now().Format("2006-01-02 15:04:05")
		for _, u := range valid {
			log.Printf("[%s] [LINK VALIDATED] %s (200)", ts, u)
			validatedSet[u] = true
		}
		for u, reason := range invReasons {
			log.Printf("[%s] [LINK INVALID] %s (%s)", ts, u, reason)
		}
		if desiredCount > 0 && len(validatedSet) >= desiredCount {
			break
		}
		if len(valid) == 0 {
			log.Printf("[%s] [FETCH LINKS] Attempt %d/%d: no valid links this round, retrying", time.Now().Format("2006-01-02 15:04:05"), attempt+1, maxRetries)
		}
	}

	validated = make([]string, 0, len(validatedSet))
	for u := range validatedSet {
		validated = append(validated, u)
	}
	if desiredCount > 0 && len(validated) > desiredCount {
		validated = validated[:desiredCount]
	}
	if len(validated) == 0 {
		if lastCallErr != nil {
			return nil, lastReqURL, lastReqBody, lastRespCode, lastRespBody, invalidReasons, lastDuration, fmt.Errorf("no validated links after %d attempt(s): %w", maxRetries, lastCallErr)
		}
		return nil, lastReqURL, lastReqBody, lastRespCode, lastRespBody, invalidReasons, lastDuration, fmt.Errorf("no validated links after %d attempt(s)", maxRetries)
	}
	return validated, lastReqURL, lastReqBody, lastRespCode, lastRespBody, invalidReasons, lastDuration, nil
}

func (c *Client) callLLM(ctx context.Context, prompt string, desiredCount int, excludeURLs []string) ([]string, string, string, int, string, time.Duration, error) {
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
				"parameters": map[string]interface{}{"enable_image_understanding": false},
			})
			hasWebSearch = true
		} else if toolType == "x_search" && !hasXSearch {
			tools = append(tools, map[string]interface{}{
				"type": "x_search",
				"parameters": map[string]interface{}{
					"from_date": fromDate, "to_date": toDate,
					"enable_image_understanding": false, "enable_video_understanding": false,
				},
			})
			hasXSearch = true
		} else if toolType != "web_search" && toolType != "x_search" && toolType != "live_search" {
			tools = append(tools, map[string]interface{}{"type": toolType, "parameters": map[string]interface{}{}})
		}
	}
	if !hasWebSearch && !hasXSearch && len(tools) == 0 {
		tools = append(tools,
			map[string]interface{}{"type": "web_search", "parameters": map[string]interface{}{"enable_image_understanding": false}},
			map[string]interface{}{"type": "x_search", "parameters": map[string]interface{}{"from_date": fromDate, "to_date": toDate, "enable_image_understanding": false, "enable_video_understanding": false}},
		)
	}
	systemText := `You are a strict news aggregation agent.

You MUST:
- Use web_search and x_search tools.
- Collect the FULL requested number of URLs before responding.
- Continue searching until all URLs are valid and meet requirements.

Rules:
- Only factual, event-based fundamental news.
- If a URL is invalid, inaccessible, duplicated, or outside the last 24 hours, discard it and continue searching.
- Prioritize search actions over reasoning.
- Do NOT stop early.

Output:
- URLs only
- One per line
- No extra text

When searching, try to collect MULTIPLE candidate URLs per search turn before filtering.`
	countNote := ""
	if desiredCount > 0 {
		countNote = fmt.Sprintf(" Target links: %d.", desiredCount)
	}
	excludeNote := ""
	if len(excludeURLs) > 0 {
		excludeNote = "\nDo NOT return any of these URLs again (already collected): " + strings.Join(excludeURLs, ", ")
	}
	userText := fmt.Sprintf("Today is %s. %s\n\nIMPORTANT: You MUST use the search tools (web_search and x_search) to find recent news. Search for news from the last 24 hours only (from %s to %s). Keep searching until you can return the full requested number of URLs.%s After searching, return ONLY URLs, one per line. No explanations, no formatting, no other text. Just the URLs.%s", toDate, prompt, fromDate, toDate, countNote, excludeNote)
	maxTurns := int(float64(desiredCount)*2.4 + 8)
	if maxTurns < 8 {
		maxTurns = 8
	}
	reqBody := map[string]interface{}{
		"model": c.Model,
		"input": []map[string]interface{}{
			{"role": "system", "content": []map[string]interface{}{{"type": "input_text", "text": systemText}}},
			{"role": "user", "content": []map[string]interface{}{{"type": "input_text", "text": userText}}},
		},
		"temperature": 0.2,
		"reasoning": map[string]string{"effort": "high"},
		"max_turns": maxTurns,
	}
	if len(tools) > 0 {
		reqBody["tools"] = tools
	}
	reqBodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, reqURL, "", 0, "", 0, err
	}
	ts := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("[%s] [GROK REQUEST] URL: %s", ts, reqURL)
	log.Printf("[%s] [GROK REQUEST] Body: %s", ts, string(reqBodyJSON))
	httpReq, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewBuffer(reqBodyJSON))
	if err != nil {
		return nil, reqURL, string(reqBodyJSON), 0, "", 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.APIKey))
	startTime := time.Now()
	httpResp, err := c.httpClient.Do(httpReq)
	duration := time.Since(startTime)
	if err != nil {
		log.Printf("[%s] [GROK ERROR] Request failed after %v: %v", ts, duration, err)
		return nil, reqURL, string(reqBodyJSON), 0, err.Error(), duration, err
	}
	defer httpResp.Body.Close()
	bodyBytes, _ := io.ReadAll(httpResp.Body)
	responseBody := string(bodyBytes)
	respCode := httpResp.StatusCode
	log.Printf("[%s] [GROK RESPONSE] Status: %d, Duration: %v", time.Now().Format("2006-01-02 15:04:05"), respCode, duration)
	log.Printf("[%s] [GROK RESPONSE] Body: %s", time.Now().Format("2006-01-02 15:04:05"), truncateString(responseBody, 1000))
	if respCode != http.StatusOK {
		return nil, reqURL, string(reqBodyJSON), respCode, responseBody, duration, fmt.Errorf("grok API error: %d - %s", respCode, responseBody)
	}
	var respEnvelope struct {
		OutputText string   `json:"output_text"`
		Citations  []string `json:"citations"`
		Output     []struct {
			Type   string `json:"type"`
			Action struct {
				Sources []struct{ URL string `json:"url"` } `json:"sources"`
			} `json:"action"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(bodyBytes, &respEnvelope); err != nil {
		return nil, reqURL, string(reqBodyJSON), respCode, responseBody, duration, err
	}
	finalContent := strings.TrimSpace(respEnvelope.OutputText)
	if finalContent == "" {
		for _, out := range respEnvelope.Output {
			if out.Type != "message" {
				continue
			}
			for _, ct := range out.Content {
				if ct.Type == "output_text" && strings.TrimSpace(ct.Text) != "" {
					finalContent = strings.TrimSpace(ct.Text)
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
		links = append(links, extractURLs(finalContent)...)
	}
	links = append(links, respEnvelope.Citations...)
	for _, out := range respEnvelope.Output {
		for _, src := range out.Action.Sources {
			if strings.TrimSpace(src.URL) != "" {
				links = append(links, strings.TrimSpace(src.URL))
			}
		}
	}
	if len(links) == 0 {
		return nil, reqURL, string(reqBodyJSON), respCode, responseBody, duration, fmt.Errorf("no links found in response output")
	}
	links = dedupeLinks(links)
	if desiredCount > 0 && len(links) > desiredCount {
		links = links[:desiredCount]
	}
	log.Printf("[%s] [GROK PARSING] Extracted %d links (requested: %d)", time.Now().Format("2006-01-02 15:04:05"), len(links), desiredCount)
	return links, reqURL, string(reqBodyJSON), respCode, responseBody, duration, nil
}

func isAssumeValidLinkError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "internal_error") ||
		strings.Contains(s, "rst_stream") ||
		strings.Contains(s, "protocol_error") ||
		strings.Contains(s, "unexpected eof") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "tls handshake timeout") ||
		strings.Contains(s, "context deadline exceeded")
}

func checkURL(u string, client *http.Client) (ok bool, reason string) {
	if client == nil {
		client = &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		}
	}
	acceptStatus := func(code int) bool { return code < 400 || code == 401 || code == 403 }
	linkCheckUserAgent := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	req, err := http.NewRequest("HEAD", u, nil)
	if err != nil {
		return false, "HEAD request build failed"
	}
	req.Header.Set("User-Agent", linkCheckUserAgent)
	resp, err := client.Do(req)
	if err != nil || (resp != nil && !acceptStatus(resp.StatusCode)) {
		if resp != nil {
			resp.Body.Close()
		}
		req, _ = http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", linkCheckUserAgent)
		resp, err = client.Do(req)
	}
	if err != nil {
		if isAssumeValidLinkError(err) {
			return true, ""
		}
		return false, fmt.Sprintf("request failed: %v", err)
	}
	if resp != nil {
		defer resp.Body.Close()
		if acceptStatus(resp.StatusCode) {
			return true, ""
		}
		return false, fmt.Sprintf("status %d", resp.StatusCode)
	}
	return false, "no response"
}

func (c *Client) validateLinks(links []string, httpClient *http.Client) (valid []string, invalidReasons map[string]string) {
	invalidReasons = make(map[string]string)
	valid = make([]string, 0)
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		}
	}
	for _, rawLink := range links {
		link := strings.TrimSpace(rawLink)
		link = strings.TrimRight(link, ".,;:!?)")
		if link == "" || len(link) <= 10 || !strings.HasPrefix(link, "http") {
			invalidReasons[rawLink] = "invalid format"
			continue
		}
		if _, err := url.Parse(link); err != nil {
			invalidReasons[link] = "parse error"
			continue
		}
		ok, reason := checkURL(link, httpClient)
		if ok {
			valid = append(valid, link)
		} else {
			invalidReasons[link] = reason
		}
	}
	return valid, invalidReasons
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
