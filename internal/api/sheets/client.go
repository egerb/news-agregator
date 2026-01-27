package sheets

import (
	"context"
	"fmt"
	"strings"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
	"news-aggregator/internal/models"
)

type Client struct {
	service      *sheets.Service
	spreadsheetID string
	sheetName     string
}

func NewClient(credentialsJSON, spreadsheetID, sheetName string) (*Client, error) {
	ctx := context.Background()

	opt := option.WithCredentialsJSON([]byte(credentialsJSON))
	service, err := sheets.NewService(ctx, opt)
	if err != nil {
		return nil, fmt.Errorf("failed to create sheets service: %w", err)
	}

	return &Client{
		service:       service,
		spreadsheetID: spreadsheetID,
		sheetName:     sheetName,
	}, nil
}

func (c *Client) ClearDataRows() error {
	readRange := fmt.Sprintf("%s!A2:Z10000", c.sheetName)
	resp, err := c.service.Spreadsheets.Values.Get(c.spreadsheetID, readRange).Do()
	if err != nil {
		return err
	}

	if len(resp.Values) == 0 {
		return nil
	}

	lastRow := len(resp.Values) + 1
	clearRange := fmt.Sprintf("%s!A2:Z%d", c.sheetName, lastRow)
	_, err = c.service.Spreadsheets.Values.Clear(c.spreadsheetID, clearRange, &sheets.ClearValuesRequest{}).Do()
	return err
}

func (c *Client) WriteNewsItems(items []models.NewsItem) error {
	if len(items) == 0 {
		return fmt.Errorf("no items to write")
	}

	var values [][]interface{}
	for _, item := range items {
		url := strings.TrimSpace(item.URL)
		topic := strings.TrimSpace(item.Topic)
		
		if url != "" && topic != "" && len(url) > 10 && strings.HasPrefix(url, "http") {
			values = append(values, []interface{}{url, topic, item.Priority})
		}
	}

	if len(values) == 0 {
		return fmt.Errorf("no valid values after filtering (had %d items)", len(items))
	}

	range_ := fmt.Sprintf("%s!B2", c.sheetName)
	valueRange := &sheets.ValueRange{
		Values: values,
	}

	result, err := c.service.Spreadsheets.Values.Update(c.spreadsheetID, range_, valueRange).
		ValueInputOption("RAW").
		Do()

	if err != nil {
		return fmt.Errorf("failed to update sheet: %w", err)
	}

	if result.UpdatedCells == 0 {
		return fmt.Errorf("no cells were updated")
	}

	return nil
}
