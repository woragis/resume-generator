package formatters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

type PublicationsFormatter struct {
	client  *http.Client
	baseURL string
	language string
}

func NewPublicationsFormatter(httpClient *http.Client, baseURL string, language string) *PublicationsFormatter {
	return &PublicationsFormatter{client: httpClient, baseURL: baseURL, language: language}
}

func (pf *PublicationsFormatter) Format(ctx context.Context, payload map[string]interface{}) (map[string]interface{}, error) {
	// Load the focused schema for publications/certifications/extras only
	schemaBytes := []byte{}
	if b, err := os.ReadFile("templates/schema/publications.schema.json"); err == nil {
		schemaBytes = b
	}
	
	instr := fmt.Sprintf("Format the output in %s. Return ONLY a single JSON object with keys 'publications', 'certifications', and 'extras' that conform to the provided schema.\n\nFor publications: return an array of descriptive strings (each >= 40 chars) in the form 'Title — YEAR. One-line summary.' If a publication item is short, expand it into a descriptive summary.\n\nFor certifications: return structured objects with fields {name (required), issuer, date (ISO), url, description (max 140 chars)} and optionally include 'url_label' as a short human-friendly label (hostname or brand).\n\nFor extras: return objects {category, text} where text <= 140 chars.\n\nDo NOT include any other fields, commentary, or non-JSON text.\n\nJSON-SCHEMA:\n", pf.language) + string(schemaBytes)
	
	userCtx := map[string]interface{}{"payload": payload, "instructions": instr}
	reqObj := map[string]interface{}{"agent": "auto", "input": "Format publications/certifications/extras:\n" + mustMarshal(userCtx)}
	b, _ := json.Marshal(reqObj)
	
	fmt.Printf("ai.client: FormatPublicationsCertsExtras POST %s/v1/chat payload=%s\n", pf.baseURL, string(b))
	
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pf.baseURL+"/v1/chat", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := pf.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	
	fmt.Printf("ai.client: FormatPublicationsCertsExtras response status=%d body=%s\n", resp.StatusCode, string(rb))
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ai-service returned non-200 status: %d", resp.StatusCode)
	}
	
	var chatResp struct {
		Agent  string `json:"agent"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(rb, &chatResp); err != nil {
		return nil, err
	}
	
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(chatResp.Output), &out); err != nil {
		// Try extracting JSON from the response if wrapped in markdown
		s := chatResp.Output
		start := -1
		end := -1
		for i, r := range s {
			if r == '{' {
				start = i
				break
			}
		}
		for i := len(s) - 1; i >= 0; i-- {
			if s[i] == '}' {
				end = i
				break
			}
		}
		if start >= 0 && end > start {
			sub := s[start : end+1]
			if err2 := json.Unmarshal([]byte(sub), &out); err2 == nil {
				return out, nil
			}
		}
		return nil, fmt.Errorf("ai-service returned non-json content: %w", err)
	}
	
	return out, nil
}
