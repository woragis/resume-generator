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

type SummaryFormatter struct {
	client  *http.Client
	baseURL string
	language string
}

func NewSummaryFormatter(httpClient *http.Client, baseURL string, language string) *SummaryFormatter {
	return &SummaryFormatter{client: httpClient, baseURL: baseURL, language: language}
}

func (sf *SummaryFormatter) Format(ctx context.Context, payload map[string]interface{}) (map[string]interface{}, error) {
	// Load the focused schema for summary and meta only
	schemaBytes := []byte{}
	if b, err := os.ReadFile("templates/schema/summary_meta.schema.json"); err == nil {
		schemaBytes = b
	}
	
	instr := fmt.Sprintf("LANGUAGE: You MUST format ALL output in %s. Translate every single field and string value into %s. Every piece of text must be in %s.\n\nReturn ONLY a single JSON object with keys 'summary' and 'meta'.\n\nCRITICAL:\n- summary: aim for 150-300 characters, MUST be in %s, prioritize meaningful professional content\n- meta.name: preserve if possible, MUST be in %s\n- meta.headline: professional headline (50-150 chars), MUST be in %s\n- meta.contact: MUST be an object {email: string, location: string}\n- Do NOT remove or change meta.social_links\n\nREMEMBER: ALL content MUST be in %s. Do NOT include any English text. Quality over strict length.\n\nJSON-SCHEMA:\n", sf.language, sf.language, sf.language, sf.language, sf.language, sf.language, sf.language) + string(schemaBytes)
	
	userCtx := map[string]interface{}{"payload": payload, "instructions": instr}
	reqObj := map[string]interface{}{"agent": "auto", "input": "Polish summary and meta:\n" + mustMarshal(userCtx)}
	b, _ := json.Marshal(reqObj)
	
	fmt.Printf("ai.client: FormatSummaryMeta POST %s/v1/chat payload=%s\n", sf.baseURL, string(b))
	
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sf.baseURL+"/v1/chat", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := sf.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	
	fmt.Printf("ai.client: FormatSummaryMeta response status=%d body=%s\n", resp.StatusCode, string(rb))
	
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
				sanitizeSummaryMeta(out)
				return out, nil
			}
		}
		return nil, fmt.Errorf("ai-service returned non-json content: %w", err)
	}
	
	// Ensure meta.contact is an object (coerce simple string emails to {"email": "..."})
	sanitizeSummaryMeta(out)
	
	return out, nil
}

// sanitizeSummaryMeta enforces proper structure for meta.contact.
// It mutates the provided map in-place.
func sanitizeSummaryMeta(m map[string]interface{}) {
	if m == nil {
		return
	}
	raw, ok := m["meta"]
	if !ok || raw == nil {
		return
	}
	meta, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	
	// If contact is a string, wrap it as an email
	if contactRaw, ok := meta["contact"]; ok {
		if contactStr, ok := contactRaw.(string); ok && contactStr != "" {
			meta["contact"] = map[string]interface{}{
				"email":    contactStr,
				"location": "",
			}
		}
	}
}
