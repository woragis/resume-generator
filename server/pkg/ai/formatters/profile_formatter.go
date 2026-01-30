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

type ProfileFormatter struct {
	client  *http.Client
	baseURL string
	language string
}

func NewProfileFormatter(httpClient *http.Client, baseURL string, language string) *ProfileFormatter {
	return &ProfileFormatter{client: httpClient, baseURL: baseURL, language: language}
}

func (pf *ProfileFormatter) Format(ctx context.Context, payload map[string]interface{}) (map[string]interface{}, error) {
	// Load the focused schema for profile/snapshot only
	schemaBytes := []byte{}
	if b, err := os.ReadFile("templates/schema/profile.schema.json"); err == nil {
		schemaBytes = b
	}
	
	instr := fmt.Sprintf("LANGUAGE: You MUST format ALL output in %s. Translate every single field and string value into %s. Every piece of text must be in %s.\n\nReturn ONLY a single JSON object with keys 'meta', 'summary', 'snapshot'.\n\nCRITICAL CONSTRAINTS:\n1. selected_projects: MUST be exactly 2 items, EACH item should be 40-200 characters (aim for quality over strict length). MUST be in %s.\n2. achievements: MUST be 3+ items, each 40+ characters. MUST be in %s.\n3. snapshot.tech: aim for 150-250 characters, prioritize meaningful content. MUST be in %s.\n4. meta.contact: MUST be an object {email: string, location: string}.\n\nREMEMBER: ALL content MUST be in %s. Do NOT include any English text. Prioritize meaningful content.\n\nJSON-SCHEMA:\n", pf.language, pf.language, pf.language, pf.language, pf.language, pf.language, pf.language) + string(schemaBytes)
	
	userCtx := map[string]interface{}{"payload": payload, "instructions": instr}
	reqObj := map[string]interface{}{"agent": "auto", "input": "Format profile and snapshot:\n" + mustMarshal(userCtx)}
	b, _ := json.Marshal(reqObj)
	
	fmt.Printf("ai.client: FormatProfileSnapshot POST %s/v1/chat payload=%s\n", pf.baseURL, string(b))
	
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
	
	fmt.Printf("ai.client: FormatProfileSnapshot response status=%d body=%s\n", resp.StatusCode, string(rb))
	
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
