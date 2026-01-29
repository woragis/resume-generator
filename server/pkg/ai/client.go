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

	"resume-generator/pkg/ai/formatters"
)

// Client calls the internal ai-service to format raw profile data into the
// canonical resume schema.
type Client struct {
	BaseURL         string
	HTTP            *http.Client
	DefaultLanguage string
}

func NewClient() *Client {
	base := os.Getenv("AI_SERVICE_URL")
	if base == "" {
		base = "http://ai-service:8000"
	}
	return &Client{BaseURL: base, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func NewClientWithLanguage(language string) *Client {
	base := os.Getenv("AI_SERVICE_URL")
	if base == "" {
		base = "http://ai-service:8000"
	}
	return &Client{BaseURL: base, HTTP: &http.Client{Timeout: 60 * time.Second}, DefaultLanguage: language}
}

// Formatter interface for the four specialized formatters
type Formatter interface {
	Format(ctx context.Context, payload map[string]interface{}) (map[string]interface{}, error)
}

// Factory methods to create formatters
func (c *Client) NewExperienceFormatter() Formatter {
	return formatters.NewExperienceFormatter(c.HTTP, c.BaseURL, c.DefaultLanguage)
}

func (c *Client) NewProfileFormatter() Formatter {
	return formatters.NewProfileFormatter(c.HTTP, c.BaseURL, c.DefaultLanguage)
}

func (c *Client) NewPublicationsFormatter() Formatter {
	return formatters.NewPublicationsFormatter(c.HTTP, c.BaseURL, c.DefaultLanguage)
}

func (c *Client) NewSummaryFormatter() Formatter {
	return formatters.NewSummaryFormatter(c.HTTP, c.BaseURL, c.DefaultLanguage)
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
 - summary: string, min 80, max 330 characters
 - snapshot.tech: string, min 10, max 180 characters
 - snapshot.achievements: array of 3 strings, each min 40, max 210 characters
 - snapshot.selected_projects: array of 2 strings, each min 40, max 150 characters
 - experience: array of objects with company (string), title (string), period (string), bullets: array of strings (each min 40, max 210)
 - projects: array of objects with id (string), title (string, max 120), url (uri), stack (string), description (80-330), bullets (array of strings 40-210)
	 - publications: array of strings, each minLength 40
	 - certifications: array of objects with fields {name: string (required), issuer: string, date: string (date), url: uri, description: string (max 210)}
	 - extras: array of objects with {category: string, text: string (max 210)}

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
	instr := "You will receive a previously validated resume JSON (base_resume) and a small set of override lists. Update ONLY the provided override fields and preserve other values. Supported override keys: publications, certifications, extras, snapshot, meta.\n\nFor publications: ensure each item is a descriptive string meeting the schema minLength; if short, expand into 'Title — YEAR. One-line summary.'\nFor certifications: return structured objects {name (required), issuer, date (ISO), url, description (<=210 chars)}.\nFor extras: return objects {category, text (<=210 chars)}.\nFor snapshot: ensure keys 'tech' (10-180 chars), 'achievements' (array with >=3 items, each >=40 chars), and 'selected_projects' (array of 2 items, each 40-150 chars). Expand or synthesize items to meet lengths as needed.\nFor meta: preserve existing meta.name if present; you may add or polish meta.headline and meta.contact but do NOT remove meta.name.\n\nReturn ONLY the full resume JSON object (same schema) and NOTHING ELSE."

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
	instr := `You will receive a small overrides object containing any of the keys: publications, certifications, extras, snapshot, meta. Return ONLY a single JSON object with those keys present (if provided) and values formatted exactly to match the schema:\n- publications -> array of descriptive strings (each >= 40 chars, e.g. "Title — YEAR. One-line summary.")\n- certifications -> array of objects {name (required), issuer, date (ISO), url, description (<=140 chars)}\n- extras -> array of objects {category, text (<=140 chars)}\n- snapshot -> object {tech: string (10-180 chars), achievements: array (>=3 items, each >=40 chars), selected_projects: array (2 items, each 40-150 chars)}\n- meta -> object; preserve meta.name if present and only add/polish headline/contact.\nDo NOT include any other fields, commentary, or formatting. If an input publication is short, expand it into a title+year+one-line summary. Example response: {"publications":["Title — 2023. One-line summary of the article's contributions."],"certifications":[{"name":"Cert A","issuer":"Org","date":"2024-01-01","url":"https://...","description":"One-line"}],"extras":[{"category":"Speaking","text":"Talk at Conf 2024"}],"snapshot":{"tech":"Go, GKE","achievements":["Achievement 1 expanded to 40+ chars...","Achievement 2 expanded to 40+ chars...","Achievement 3 expanded to 40+ chars..."],"selected_projects":["Project 1 — short summary 40+ chars","Project 2 — short summary 40+ chars"]}}`

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

// FormatExperienceProjects calls the AI to produce only the experience and
// projects sections. It returns a map with keys "experience" and "projects".
// This now delegates to the ExperienceFormatter.
func (c *Client) FormatExperienceProjects(ctx context.Context, payload map[string]interface{}) (map[string]interface{}, error) {
	formatter := c.NewExperienceFormatter()
	return formatter.Format(ctx, payload)
}

// FormatProfileSnapshot returns profile/meta/summary and snapshot fields.
// This now delegates to the ProfileFormatter.
func (c *Client) FormatProfileSnapshot(ctx context.Context, payload map[string]interface{}) (map[string]interface{}, error) {
	formatter := c.NewProfileFormatter()
	return formatter.Format(ctx, payload)
}

// FormatPublicationsCertsExtras returns publications/certifications/extras only.
// This now delegates to the PublicationsFormatter.
func (c *Client) FormatPublicationsCertsExtras(ctx context.Context, payload map[string]interface{}) (map[string]interface{}, error) {
	formatter := c.NewPublicationsFormatter()
	return formatter.Format(ctx, payload)
}

// FormatSummaryMeta returns a short polished summary and headline only.
// This now delegates to the SummaryFormatter.
func (c *Client) FormatSummaryMeta(ctx context.Context, payload map[string]interface{}) (map[string]interface{}, error) {
	formatter := c.NewSummaryFormatter()
	return formatter.Format(ctx, payload)
}

// mustMarshal is a tiny helper for embedding example payloads in prompts.
func mustMarshal(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// sanitizeCertifications enforces date formats and description length for
// the 'certifications' field produced by the AI. It mutates the provided
// map in-place.
func sanitizeCertifications(m map[string]interface{}) {
	if m == nil {
		return
	}
	raw, ok := m["certifications"]
	if !ok || raw == nil {
		return
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return
	}
	for i := range arr {
		item, ok := arr[i].(map[string]interface{})
		if !ok {
			continue
		}
		// Normalize date
		if d, ok := item["date"].(string); ok {
			if len(d) == 7 { // YYYY-MM -> YYYY-MM-01
				item["date"] = d + "-01"
			} else if len(d) == 4 { // YYYY -> YYYY-01-01
				item["date"] = d + "-01-01"
			}
		}
		// Truncate description to 140 chars without cutting words
		if descRaw, ok := item["description"].(string); ok && len(descRaw) > 140 {
			truncated := descRaw[:140]
			// find last space to avoid cutting in middle of word
			last := -1
			for idx := len(truncated) - 1; idx >= 0; idx-- {
				if truncated[idx] == ' ' {
					last = idx
					break
				}
			}
			if last > 0 {
				truncated = truncated[:last]
			}
			item["description"] = truncated
		}
		// leave url as-is if present
		arr[i] = item
	}
	m["certifications"] = arr
}

// sanitizeSummaryMeta coerces meta.contact from a simple string into an
// object {"email": "..."} which downstream schema validation expects.
func sanitizeSummaryMeta(m map[string]interface{}) {
	if m == nil {
		return
	}
	metaRaw, ok := m["meta"]
	if !ok || metaRaw == nil {
		return
	}
	meta, ok := metaRaw.(map[string]interface{})
	if !ok {
		return
	}
	if contactRaw, ok := meta["contact"]; ok {
		switch v := contactRaw.(type) {
		case string:
			// simple string -> {"email": "..."}
			meta["contact"] = map[string]interface{}{"email": v}
		case map[string]interface{}:
			// already structured; nothing to do
		default:
			// leave as-is for unknown types
		}
	}
	m["meta"] = meta
}
