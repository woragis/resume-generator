package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Client calls the internal ai-service to format raw profile data into the
// canonical resume schema.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient() *Client {
	base := os.Getenv("AI_SERVICE_URL")
	if base == "" {
		base = "http://ai-service:8000"
	}
	return &Client{BaseURL: base, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// doPostWithRetry performs an HTTP POST to the given path with retry/backoff.
func (c *Client) doPostWithRetry(ctx context.Context, path string, body []byte) (*http.Response, error) {
	attempts := 3
	var lastErr error
	for i := 0; i < attempts; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.HTTP.Do(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		// exponential backoff before retrying
		if i < attempts-1 {
			backoff := time.Duration(1<<i) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return nil, lastErr
}

// FormatResume sends rawProfile to the ai-service and attempts to obtain a
// structured resume. It calls the content generation endpoint with type
// "profile" and places rawProfile into userContext. If the returned
// content is JSON, it is unmarshaled and returned as the resume map.
func (c *Client) FormatResume(ctx context.Context, rawProfile interface{}) (map[string]interface{}, []string, bool, error) {
	// Build a userContext that includes strict instructions asking
	// the ai-service to return only a JSON object matching our schema.
	instructions := "Respond with ONLY a single JSON object that conforms to the resume JSON Schema. Do NOT include any explanatory text, backticks, or code fences. If you include anything else, the caller will fail."

	// Try to load the JSON schema file and append it to the instructions
	// to make the requirement explicit to the LLM. If the file isn't
	// available, fall back to the brief instruction above.
	if schemaBytes, err := os.ReadFile("templates/resume.schema.json"); err == nil {
		instructions = instructions + "\n\nJSON-SCHEMA:\n" + string(schemaBytes)
	}

	userCtx := map[string]interface{}{
		"profile":      rawProfile,
		"instructions": instructions,
	}

	// Build a chat prompt that includes the strict instruction and the
	// user context. We'll call the ai-service chat endpoint which
	// accepts an explicit system/input style request and is less
	// likely to apply a service-side template that alters output.
	promptObj := map[string]interface{}{
		"userContext": userCtx,
	}
	promptBytes, _ := json.Marshal(promptObj)
	// Add explicit per-field length constraints and a strict JSON skeleton
	// to reduce ambiguity for the LLM and force outputs that validate.
	constraints := `Strict field constraints (enforce exactly):
 - meta.name: string (required)
 - meta.headline: string (required)
 - summary: string, min 80, max 220 characters
 - snapshot.tech: string, min 10, max 120 characters
 - snapshot.achievements: array of 3 strings, each min 40, max 140 characters
 - snapshot.selected_projects: array of 2 strings, each min 40, max 100 characters
 - experience: array of objects with company (string), title (string), period (string), bullets: array of strings (each min 40, max 140)
 - projects: array of objects with id (string), title (string, max 80), url (uri), stack (string), description (80-220), bullets (array of strings 40-140)
	 - publications: array of strings, each minLength 40
	 - certifications: array of objects with fields {name: string (required), issuer: string, date: string (date), url: uri, description: string (max 140)}
	 - extras: array of objects with {category: string, text: string (max 140)}

If any field would exceed the max length, you MUST shorten or summarize the text so it fits the max.
You MUST return ONLY valid JSON (a single object) and NOTHING ELSE — no commentary, no markdown, no code fences.

Example JSON skeleton (use this structure and follow the length limits):
{
	"meta": {"name": "NAME", "headline": "SHORT HEADLINE", "contact": {"email": "x@x.com"}},
	"summary": "A short summary between 80 and 220 chars...",
	"snapshot": {"tech": "Comma-separated tech list", "achievements": ["ach1","ach2","ach3"], "selected_projects": ["proj short 1","proj short 2"]},
	"experience": [{"company":"Org","title":"Role","period":"YYYY–YYYY","bullets":["accomplishment 1","accomplishment 2"]}],
	"projects": [{"id":"p1","title":"Short title","url":"https://...","stack":"Go, Postgres","description":"80-220 char description...","bullets":["impact 1","impact 2"]}],
	"job_application": {"job_title":"Senior Backend Engineer","company_name":"Acme Tech","job_description":"Build Go microservices and improve throughput","job_url":"https://acme.example/jobs/123"},
	"publications": ["Title — YEAR. One-line summary."],
	"certifications": [{"name": "Certified X", "issuer": "Org", "date": "2024-01-01", "url": "https://...", "description": "One-line summary"}],
	"extras": [{"category": "Open Source", "text": "Maintainer of project X"}]
}
`

	prompt := "You will produce EXACTLY one JSON object and NOTHING ELSE. The object must conform to the provided JSON Schema and the field length rules below. Do not include any extra text, explanations, or Markdown. Output must be valid JSON only.\n\n" + constraints + "\n\nContext:\n" + string(promptBytes)

	chatReq := map[string]interface{}{
		"agent": "auto",
		"input": prompt,
	}
	b, err := json.Marshal(chatReq)
	if err != nil {
		return nil, nil, false, err
	}

	// Debug: log outgoing request payload
	fmt.Printf("ai.client: POST %s/v1/chat payload=%s\n", c.BaseURL, string(b))

	resp, err := c.doPostWithRetry(ctx, "/v1/chat", b)
	if err != nil {
		return nil, nil, false, err
	}
	defer resp.Body.Close()

	// Read and log the raw response body for debugging
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, false, err
	}
	fmt.Printf("ai.client: response status=%d body=%s\n", resp.StatusCode, string(respBytes))

	if resp.StatusCode != http.StatusOK {
		return nil, nil, false, errors.New("ai-service returned non-200 status")
	}

	var chatResp struct {
		Agent  string `json:"agent"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return nil, nil, false, err
	}

	// Try to parse the chat output as JSON; if it fails, attempt robust extraction
	var resumeMap map[string]interface{}
	if err := json.Unmarshal([]byte(chatResp.Output), &resumeMap); err != nil {
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
			if err2 := json.Unmarshal([]byte(sub), &resumeMap); err2 == nil {
				return resumeMap, nil, false, nil
			}
		}
		return nil, nil, false, fmt.Errorf("ai-service returned non-json content: %w", err)
	}

	// This endpoint doesn't return structured warnings/synthesized flags.
	return resumeMap, nil, false, nil
}

// EnrichResume receives a previously validated base resume and a small
// overrides map containing publications/certifications/extras. It asks the
// ai-service to preserve and, if necessary, expand those override items to
// meet schema constraints without changing other sections.
func (c *Client) EnrichResume(ctx context.Context, baseResume map[string]interface{}, overrides map[string]interface{}) (map[string]interface{}, error) {
	instr := "You will receive a previously validated resume JSON (base_resume) and a small set of override lists (publications, certifications, extras). Update ONLY the fields publications, certifications, extras: preserve existing values. Ensure publications are strings meeting the schema minLength; if short, expand them into 'Title — YEAR. One-line summary.' For certifications, return structured objects with fields {name, issuer, date, url, description} (name required). For extras, return an array of objects {category, text}. Do NOT modify other fields. Return ONLY the full resume JSON object (same schema) and NOTHING ELSE."

	payloadObj := map[string]interface{}{
		"base_resume":  baseResume,
		"overrides":    overrides,
		"instructions": instr,
	}
	b, err := json.Marshal(map[string]interface{}{"userContext": payloadObj})
	if err != nil {
		return nil, err
	}

	chatReq := map[string]interface{}{
		"agent": "auto",
		"input": "Enrich resume with overrides:\n" + string(b),
	}
	rb, err := json.Marshal(chatReq)
	if err != nil {
		return nil, err
	}

	fmt.Printf("ai.client: ENRICH POST %s/v1/chat payload=%s\n", c.BaseURL, string(rb))

	resp, err := c.doPostWithRetry(ctx, "/v1/chat", rb)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	fmt.Printf("ai.client: enrich response status=%d body=%s\n", resp.StatusCode, string(respBytes))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ai-service returned non-200 status: %d", resp.StatusCode)
	}

	var chatResp struct {
		Agent  string `json:"agent"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return nil, err
	}

	var enriched map[string]interface{}
	if err := json.Unmarshal([]byte(chatResp.Output), &enriched); err != nil {
		// try to extract JSON substring
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
			if err2 := json.Unmarshal([]byte(sub), &enriched); err2 == nil {
				return enriched, nil
			}
		}
		return nil, fmt.Errorf("ai-service returned non-json content: %w", err)
	}

	return enriched, nil
}

// EnrichFields asks the AI to return ONLY the specific override fields
// (publications, certifications, extras) as a JSON object. This reduces
// risk of modifying other parts of the resume and makes targeted merging
// safer.
func (c *Client) EnrichFields(ctx context.Context, overrides map[string]interface{}) (map[string]interface{}, error) {
	instr := `You will receive a small overrides object containing any of the keys: publications, certifications, extras. Return ONLY a single JSON object with those keys present (if provided) and values formatted exactly to match the schema: publications -> array of strings, certifications -> array of objects {name, issuer, date, url, description}, extras -> array of objects {category, text}. Do NOT include any other fields, commentary, or formatting. Example response: {"publications":["Title — YEAR. One-line summary."],"certifications":[{"name":"Cert A","issuer":"Org","date":"2024-01-01","url":"https://...","description":"One-line"}],"extras":[{"category":"Speaking","text":"Talk at Conf 2024"}]}`

	payloadObj := map[string]interface{}{
		"overrides":    overrides,
		"instructions": instr,
	}
	b, err := json.Marshal(map[string]interface{}{"userContext": payloadObj})
	if err != nil {
		return nil, err
	}

	chatReq := map[string]interface{}{
		"agent": "auto",
		"input": "Enrich only specific fields:\n" + string(b),
	}
	rb, err := json.Marshal(chatReq)
	if err != nil {
		return nil, err
	}

	fmt.Printf("ai.client: ENRICH_FIELDS POST %s/v1/chat payload=%s\n", c.BaseURL, string(rb))

	resp, err := c.doPostWithRetry(ctx, "/v1/chat", rb)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	fmt.Printf("ai.client: enrich_fields response status=%d body=%s\n", resp.StatusCode, string(respBytes))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ai-service returned non-200 status: %d", resp.StatusCode)
	}

	var chatResp struct {
		Agent  string `json:"agent"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return nil, err
	}

	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(chatResp.Output), &fields); err != nil {
		// try substring extraction
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
			if err2 := json.Unmarshal([]byte(sub), &fields); err2 == nil {
				return fields, nil
			}
		}
		return nil, fmt.Errorf("ai-service returned non-json content: %w", err)
	}

	return fields, nil
}
