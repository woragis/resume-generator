package formatters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type LabelsFormatter struct {
	client   *http.Client
	baseURL  string
	language string
}

func NewLabelsFormatter(httpClient *http.Client, baseURL string, language string) *LabelsFormatter {
	return &LabelsFormatter{client: httpClient, baseURL: baseURL, language: language}
}

func (lf *LabelsFormatter) Format(ctx context.Context) (map[string]string, error) {
	instr := fmt.Sprintf(`You are a professional resume label translator. Translate section headings to %s.

RULES:
1. Return ONLY valid JSON (no markdown, no code blocks, no explanation)
2. Translate VALUES to %s ONLY - do NOT change the KEY names
3. Each value must be a professional heading (1-5 words)
4. Do NOT return snake_case - return proper %s language
5. MUST include ALL 12 keys in the output

REQUIRED OUTPUT FORMAT (with all 12 keys):
{
  "professional_summary": "<translated heading>",
  "tech_snapshot": "<translated heading>",
  "top_achievements": "<translated heading>",
  "selected_projects": "<translated heading>",
  "experience": "<translated heading>",
  "projects_case_studies": "<translated heading>",
  "publications": "<translated heading>",
  "certifications": "<translated heading>",
  "continuous_learning_community": "<translated heading>",
  "extras": "<translated heading>",
  "page_2_projects_publications": "<translated heading>",
  "references_available": "<translated heading>"
}

Example for Portuguese:
{
  "professional_summary": "Resumo Profissional",
  "tech_snapshot": "Visão Geral Técnica",
  "top_achievements": "Principais Conquistas",
  "selected_projects": "Projetos Selecionados",
  "experience": "Experiência",
  "projects_case_studies": "Projetos — Estudos de Caso",
  "publications": "Publicações",
  "certifications": "Certificações",
  "continuous_learning_community": "Aprendizado Contínuo e Comunidade",
  "extras": "Extras",
  "page_2_projects_publications": "Página 2 — Projetos e Publicações",
  "references_available": "Referências Disponíveis"
}

NOW translate to %s. Return ONLY JSON with all 13 keys.`, lf.language, lf.language, lf.language, lf.language)

	reqObj := map[string]interface{}{"agent": "auto", "input": "Translate UI labels to " + lf.language + ":\n" + instr}
	b, _ := json.Marshal(reqObj)

	fmt.Printf("ai.client: FormatLabels POST %s/v1/chat payload=%s\n", lf.baseURL, string(b))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, lf.baseURL+"/v1/chat", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := lf.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	fmt.Printf("ai.client: FormatLabels response status=%d body=%s\n", resp.StatusCode, string(rb))

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

	var out map[string]string
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

// GetDefaultLabels returns English labels as fallback
func GetDefaultLabels() map[string]string {
	return map[string]string{
		"professional_summary":     "Professional Summary",
		"tech_snapshot":            "Tech Snapshot",
		"top_achievements":         "Top Achievements",
		"selected_projects":        "Selected Projects",
		"experience":               "Experience",
		"projects_case_studies":    "Projects — Case Studies",
		"publications":             "Publications",
		"certifications":           "Certifications",
		"continuous_learning_community": "Continuous Learning & Community",
		"extras":                   "Extras",
		"page_2_projects_publications": "Page 2 — Projects & Publications",
		"references_available":     "References available on request",
	}
}
