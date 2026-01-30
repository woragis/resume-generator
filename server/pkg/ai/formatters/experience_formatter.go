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

type ExperienceFormatter struct {
	client *http.Client
	baseURL string
	language string
}

func NewExperienceFormatter(httpClient *http.Client, baseURL string, language string) *ExperienceFormatter {
	return &ExperienceFormatter{client: httpClient, baseURL: baseURL, language: language}
}

func (ef *ExperienceFormatter) Format(ctx context.Context, payload map[string]interface{}) (map[string]interface{}, error) {
	// Load the focused schema for experience + projects only
	schemaBytes := []byte{}
	if b, err := os.ReadFile("templates/schema/experience.schema.json"); err == nil {
		schemaBytes = b
	}
	
	instr := fmt.Sprintf("LANGUAGE: You MUST format ALL output in %s. Translate every single field and string value into %s. Every piece of text must be in %s.\n\nReturn ONLY a single JSON object with keys 'experience' and 'projects' that conform to the provided schema. For each experience entry include an optional 'summary' field: a meaningful paragraph (aim for 100-300 characters) describing the role and impact.\n\nIMPORTANT: For projects that do NOT have a 'url' field or have a null/empty url, use the user's GitHub link provided in the payload (aggregated.profiles[0].social_links.github). This is the default link for projects without their own URL.\n\nDo NOT include any extra text beyond the JSON.\n\nREMEMBER: ALL content MUST be in %s. Do NOT include any English text. Prioritize meaningful content.\n\nJSON-SCHEMA:\n", ef.language, ef.language, ef.language, ef.language) + string(schemaBytes)
	
	userCtx := map[string]interface{}{"payload": payload, "instructions": instr}
	reqObj := map[string]interface{}{"agent": "auto", "input": "Format experience and projects:\n" + mustMarshal(userCtx)}
	b, _ := json.Marshal(reqObj)
	
	fmt.Printf("ai.client: FormatExperienceProjects POST %s/v1/chat payload=%s\n", ef.baseURL, string(b))
	
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ef.baseURL+"/v1/chat", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := ef.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	
	fmt.Printf("ai.client: FormatExperienceProjects response status=%d body=%s\n", resp.StatusCode, string(rb))
	
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

// mustMarshal is a helper for embedding payloads in prompts
func mustMarshal(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
