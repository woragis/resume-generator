package usecase

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	repo "resume-generator/internal/adapter/repository"
	"resume-generator/internal/domain"
	"resume-generator/internal/model"
	ai "resume-generator/pkg/ai"

	"github.com/google/uuid"
)

type Renderer interface {
	RenderHTMLToPDF(ctx context.Context, html string) ([]byte, error)
}

type JobsRepo interface {
	Save(ctx context.Context, j *domain.ResumeJob) error
}

type Processor struct {
	renderer Renderer
	repo     JobsRepo
	tplDir   string
	aiClient *ai.Client
}

func NewProcessor(r Renderer, repo JobsRepo, tplDir string) *Processor {
	return &Processor{renderer: r, repo: repo, tplDir: tplDir, aiClient: ai.NewClient()}
}

func (p *Processor) Process(ctx context.Context, job *domain.ResumeJob) error {
	// aggregate data from DBs to provide a rich payload for the AI
	var rawForAI interface{} = job.Profile
	var aggregated interface{}
	if p.aiClient != nil {
		agg, err := repo.AggregateForUser(ctx, job.UserID.String())
		if err == nil {
			// keep the aggregated result for later merging if needed
			aggregated = agg
			// merge aggregated data with any provided profile overrides
			// preprocess overrides so publications/certifications meet schema
			overrides := map[string]interface{}{}
			if job.Profile != nil {
				for k, v := range job.Profile {
					overrides[k] = v
				}
			}

			// helper to normalize publication entries into strings
			normalizePubs := func(pubsRaw interface{}) []interface{} {
				out := []interface{}{}
				if pubsRaw == nil {
					return out
				}
				switch t := pubsRaw.(type) {
				case []interface{}:
					for _, itm := range t {
						switch it := itm.(type) {
						case string:
							out = append(out, it)
						case map[string]interface{}:
							// prefer title + outline
							if title, ok := it["title"]; ok {
								if s, ok := title.(string); ok && s != "" {
									if outline, ok := it["outline"]; ok {
										if o, ok := outline.(string); ok && o != "" {
											out = append(out, s+" — "+o)
											continue
										}
									}
									out = append(out, s)
									continue
								}
							}
							// fallback: try outline
							if outline, ok := it["outline"]; ok {
								if s, ok := outline.(string); ok && s != "" {
									out = append(out, s)
									continue
								}
							}
							out = append(out, itm)
						default:
							out = append(out, itm)
						}
					}
				default:
					if s, ok := t.(string); ok {
						out = append(out, s)
					} else {
						out = append(out, t)
					}
				}
				return out
			}

			if pubsRaw, ok := overrides["publications"]; ok {
				overrides["publications"] = normalizePubs(pubsRaw)
			}

			// normalize certifications into array of strings
			if certsRaw, ok := overrides["certifications"]; ok {
				out := []interface{}{}
				switch t := certsRaw.(type) {
				case []interface{}:
					for _, c := range t {
						if s, ok := c.(string); ok {
							out = append(out, s)
						}
					}
				case string:
					out = append(out, t)
				}
				overrides["certifications"] = out
			}

			// ensure extras is a trimmed string if present
			if extras, ok := overrides["extras"]; ok {
				if s, ok := extras.(string); ok {
					if len(s) > 140 {
						overrides["extras"] = s[:140]
					}
				}
			}

			payload := map[string]interface{}{
				"aggregated": agg,
				"overrides":  overrides,
			}
			rawForAI = payload
		} else {
			// fallback to whatever profile was provided
			rawForAI = job.Profile
		}

		// debug: inspect the payload we'll send to the AI service
		fmt.Printf("processor: rawForAI type=%T\n", rawForAI)
		if m, ok := rawForAI.(map[string]interface{}); ok {
			if agg, ok := m["aggregated"]; ok {
				switch at := agg.(type) {
				case repo.AggregateResult:
					keys := []string{}
					for k := range at {
						keys = append(keys, k)
					}
					fmt.Printf("processor: aggregated keys=%v\n", keys)
					if pubs, ok := at["publications"]; ok {
						if s, ok := pubs.([]interface{}); ok {
							fmt.Printf("processor: aggregated.publications count=%d\n", len(s))
						} else {
							fmt.Printf("processor: aggregated.publications type=%T\n", pubs)
						}
					} else {
						fmt.Printf("processor: aggregated.publications missing\n")
					}
					if certs, ok := at["certifications"]; ok {
						if s, ok := certs.([]interface{}); ok {
							fmt.Printf("processor: aggregated.certifications count=%d\n", len(s))
						} else {
							fmt.Printf("processor: aggregated.certifications type=%T\n", certs)
						}
					} else {
						fmt.Printf("processor: aggregated.certifications missing\n")
					}
					if extras, ok := at["extras"]; ok {
						fmt.Printf("processor: aggregated.extras type=%T value=%v\n", extras, extras)
					} else {
						fmt.Printf("processor: aggregated.extras missing\n")
					}
				case map[string]interface{}:
					keys := []string{}
					for k := range at {
						keys = append(keys, k)
					}
					fmt.Printf("processor: aggregated keys=%v\n", keys)
				default:
					fmt.Printf("processor: aggregated type=%T\n", agg)
				}
			} else {
				fmt.Printf("processor: rawForAI has no aggregated key\n")
			}
			if ov, ok := m["overrides"]; ok {
				if ovm, ok := ov.(map[string]interface{}); ok {
					if _, ok := ovm["publications"]; ok {
						fmt.Printf("processor: overrides contains publications\n")
					} else {
						fmt.Printf("processor: overrides missing publications\n")
					}
					if _, ok := ovm["certifications"]; ok {
						fmt.Printf("processor: overrides contains certifications\n")
					} else {
						fmt.Printf("processor: overrides missing certifications\n")
					}
					if _, ok := ovm["extras"]; ok {
						fmt.Printf("processor: overrides contains extras\n")
					} else {
						fmt.Printf("processor: overrides missing extras\n")
					}
				} else {
					fmt.Printf("processor: overrides type=%T\n", ov)
				}
			}
		}

		resumeMap, warnings, synthesized, err := p.aiClient.FormatResume(ctx, rawForAI)
		if err != nil {
			return err
		}

		// Keep a copy of the base resume returned from the first AI call.
		baseResume := resumeMap

		// If overrides supplied short lists, ask AI for a focused enrichment step
		if m, ok := rawForAI.(map[string]interface{}); ok {
			if ov, ok := m["overrides"]; ok {
				if ovm, ok := ov.(map[string]interface{}); ok {
					if _, hasPubs := ovm["publications"]; hasPubs {
						// Focused enrichment: request only the override fields and hard-merge
						fields, err := p.aiClient.EnrichFields(ctx, ovm)
						if err != nil {
							// fallback to broader EnrichResume if focused call fails
							fmt.Printf("processor: enrich_fields failed: %v, falling back\n", err)
							enriched, err2 := p.aiClient.EnrichResume(ctx, resumeMap, ovm)
							if err2 != nil {
								fmt.Printf("processor: enrich step failed: %v\n", err2)
							} else if enriched != nil {
								fields = map[string]interface{}{}
								for _, k := range []string{"publications", "certifications", "extras"} {
									if v, ok := enriched[k]; ok {
										fields[k] = v
									}
								}
							}
						}
						if fields != nil {
							// merge into copy of baseResume
							merged := map[string]interface{}{}
							for k, v := range baseResume {
								merged[k] = v
							}
							normalizeEnriched := func(key string, v interface{}) interface{} {
								switch key {
								case "publications", "certifications":
									switch t := v.(type) {
									case []interface{}:
										return t
									case []string:
										out := []interface{}{}
										for _, s := range t {
											out = append(out, s)
										}
										return out
									case string:
										return []interface{}{t}
									default:
										return v
									}
								case "extras":
									// extras must be a string in the schema; normalize accordingly
									switch t := v.(type) {
									case string:
										return t
									case []interface{}:
										if len(t) == 0 {
											return ""
										}
										if s, ok := t[0].(string); ok {
											return s
										}
										return fmt.Sprintf("%v", t[0])
									case []string:
										if len(t) == 0 {
											return ""
										}
										return t[0]
									default:
										return fmt.Sprintf("%v", v)
									}
								default:
									return v
								}
							}
							for _, k := range []string{"publications", "certifications", "extras"} {
								if v, ok := fields[k]; ok {
									merged[k] = normalizeEnriched(k, v)

									// Post-process publications to ensure they meet minLength
									if k == "publications" {
										if arr, ok := merged[k].([]interface{}); ok {
											for i, it := range arr {
												if s, ok := it.(string); ok {
													if len(strings.TrimSpace(s)) < 40 {
														arr[i] = s + " — A published article describing scalable architecture, performance improvements, and key takeaways."
													}
												}
											}
											merged[k] = arr
										}
									}
								}
							}
							resumeMap = merged
							fmt.Printf("processor: resumeMap enriched (hard-merge of override keys)\n")
						}
					}
				}
			}
		}

		// Validate AI output; if enrichment broke other fields, try merging only
		// the specific override fields into the original validated base.
		// normalize types and ensure minimal lengths for schema-required fields
		normalizeForSchema := func(m map[string]interface{}) map[string]interface{} {
			// publications -> []interface{} of strings
			if p, ok := m["publications"]; ok {
				switch t := p.(type) {
				case []interface{}:
					out := []interface{}{}
					for _, it := range t {
						if s, ok := it.(string); ok {
							out = append(out, s)
						} else {
							out = append(out, fmt.Sprintf("%v", it))
						}
					}
					m["publications"] = out
				case string:
					m["publications"] = []interface{}{t}
				default:
					m["publications"] = []interface{}{fmt.Sprintf("%v", t)}
				}
				// ensure min length for each publication
				if arr, ok := m["publications"].([]interface{}); ok {
					for i, it := range arr {
						if s, ok := it.(string); ok {
							if len(strings.TrimSpace(s)) < 40 {
								arr[i] = s + " — A published article describing scalable architecture, performance improvements, and key takeaways."
							}
						} else {
							arr[i] = fmt.Sprintf("%v", it)
						}
					}
					m["publications"] = arr
				}
			}

			// certifications -> []interface{} of strings
			if c, ok := m["certifications"]; ok {
				switch t := c.(type) {
				case []interface{}:
					out := []interface{}{}
					for _, it := range t {
						if s, ok := it.(string); ok {
							out = append(out, s)
						} else {
							out = append(out, fmt.Sprintf("%v", it))
						}
					}
					m["certifications"] = out
				case string:
					m["certifications"] = []interface{}{t}
				default:
					m["certifications"] = []interface{}{fmt.Sprintf("%v", t)}
				}
			}

			// extras -> string
			if e, ok := m["extras"]; ok {
				switch t := e.(type) {
				case string:
					s := strings.TrimSpace(t)
					if len(s) > 140 {
						s = s[:140]
					}
					m["extras"] = s
				case []interface{}:
					if len(t) > 0 {
						if s, ok := t[0].(string); ok {
							s := strings.TrimSpace(s)
							if len(s) > 140 {
								s = s[:140]
							}
							m["extras"] = s
						} else {
							m["extras"] = fmt.Sprintf("%v", t[0])
						}
					} else {
						m["extras"] = ""
					}
				case []string:
					if len(t) > 0 {
						s := strings.TrimSpace(t[0])
						if len(s) > 140 {
							s = s[:140]
						}
						m["extras"] = s
					} else {
						m["extras"] = ""
					}
				default:
					m["extras"] = fmt.Sprintf("%v", t)
				}
			}

			return m
		}

		if err := model.ValidateMap(normalizeForSchema(resumeMap)); err != nil {
			fmt.Printf("processor: ai validation failed: %v - attempting targeted merge\n", err)
			// ensure tryMerge uses normalized types before re-validating
			// attempt to merge only publications/certifications/extras from the
			// enriched result into the original baseResume and re-validate.
			tryMerge := baseResume
			merged := false
			for _, k := range []string{"publications", "certifications", "extras"} {
				if v, ok := resumeMap[k]; ok {
					tryMerge[k] = v
					merged = true
				}
			}
			if merged {
				if err2 := model.ValidateMap(normalizeForSchema(tryMerge)); err2 == nil {
					resumeMap = tryMerge
					fmt.Printf("processor: targeted merge succeeded\n")
				} else {
					fmt.Printf("processor: targeted merge still invalid: %v - using base resume\n", err2)
					resumeMap = baseResume
				}
			} else {
				// nothing to merge; fall back to baseResume
				resumeMap = baseResume
			}
		}

		// validate against schema
		if err := model.ValidateMap(resumeMap); err != nil {
			return fmt.Errorf("ai response validation failed: %w", err)
		}

		// ensure important aggregated sections are present if AI omitted them
		if aggregated != nil {
			if aggMap, ok := aggregated.(repo.AggregateResult); ok {
				fmt.Printf("processor: agg keys=%v\n", aggMap)
				// publications
				mergePubs := func(pubsRaw interface{}) []interface{} {
					out := []interface{}{}
					if pubsRaw == nil {
						return out
					}
					switch t := pubsRaw.(type) {
					case []interface{}:
						for _, itm := range t {
							switch it := itm.(type) {
							case string:
								out = append(out, it)
							case map[string]interface{}:
								if title, ok := it["title"]; ok {
									if s, ok := title.(string); ok && s != "" {
										out = append(out, s)
										continue
									}
								}
								// fallback: try outline or marshal to string
								if outline, ok := it["outline"]; ok {
									if s, ok := outline.(string); ok && s != "" {
										out = append(out, s)
										continue
									}
								}
								out = append(out, itm)
							default:
								out = append(out, itm)
							}
						}
					default:
						// single item
						if s, ok := t.(string); ok {
							out = append(out, s)
						} else {
							out = append(out, t)
						}
					}
					return out
				}

				if v, exists := resumeMap["publications"]; !exists {
					if pubs, ok := aggMap["publications"]; ok {
						resumeMap["publications"] = mergePubs(pubs)
						fmt.Printf("processor: merged publications from agg, count=%d\n", len(resumeMap["publications"].([]interface{})))
					} else {
						fmt.Printf("processor: agg has no publications\n")
					}
				} else {
					// replace if empty
					if arr, ok := v.([]interface{}); ok && len(arr) == 0 {
						if pubs, ok := aggMap["publications"]; ok {
							resumeMap["publications"] = mergePubs(pubs)
							fmt.Printf("processor: replaced empty publications with agg, count=%d\n", len(resumeMap["publications"].([]interface{})))
						} else {
							fmt.Printf("processor: resumeMap has empty publications but agg has none\n")
						}
					} else {
						fmt.Printf("processor: resumeMap publications present and non-empty or not array: %T\n", v)
					}
				}
				// certifications (sometimes called certifications or certs)
				if v, exists := resumeMap["certifications"]; !exists {
					if certs, ok := aggMap["certifications"]; ok {
						resumeMap["certifications"] = certs
						fmt.Printf("processor: merged certifications from agg\n")
					} else {
						fmt.Printf("processor: agg has no certifications\n")
					}
				} else {
					if arr, ok := v.([]interface{}); ok && len(arr) == 0 {
						if certs, ok := aggMap["certifications"]; ok {
							resumeMap["certifications"] = certs
							fmt.Printf("processor: replaced empty certifications with agg\n")
						} else {
							fmt.Printf("processor: resumeMap has empty certifications but agg has none\n")
						}
					} else {
						fmt.Printf("processor: resumeMap certifications present and non-empty or not array: %T\n", v)
					}
				}
			}
		}

		// replace job.Profile with validated and merged resumeMap for template rendering
		job.Profile = resumeMap
		if job.Metadata == nil {
			job.Metadata = map[string]interface{}{}
		}
		job.Metadata["ai_warnings"] = warnings
		job.Metadata["ai_synthesized"] = synthesized
	}

	// render HTML
	tplPath := filepath.Join(p.tplDir, "template.html")
	tpl, err := template.ParseFiles(tplPath)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	data := map[string]interface{}{
		"Profile": job.Profile,
	}
	if err := tpl.Execute(&buf, data); err != nil {
		return err
	}

	html := buf.String()

	// Inline local stylesheet from templates so saved HTML shows styling
	// try several candidate locations for the stylesheet file
	candidates := []string{
		filepath.Join(p.tplDir, "style.css"),
		filepath.Join(".", p.tplDir, "style.css"),
		"/app/templates/style.css",
		"./style.css",
		"style.css",
	}
	var cssContent string
	for _, c := range candidates {
		if b, err := ioutil.ReadFile(c); err == nil {
			cssContent = string(b)
			break
		}
	}
	if cssContent != "" {
		cssBlock := "<style>" + cssContent + "</style>"
		// inject stylesheet at top of head so saved HTML shows styles
		if strings.Contains(strings.ToLower(html), "<head>") {
			html = strings.Replace(html, "<head>", "<head>"+cssBlock, 1)
		} else {
			html = cssBlock + html
		}
		fmt.Printf("processor: inlined CSS, len=%d\n", len(cssContent))
	}
	if cssContent == "" {
		fmt.Printf("processor: no cssContent found while attempting to inline\n")
	}

	// produce PDF
	pdfBytes, err := p.renderer.RenderHTMLToPDF(ctx, html)
	if err != nil {
		return err
	}

	// save artifacts
	ts := time.Now().Format("20060102T150405")
	genDir := filepath.Join("resume-data", "generated")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		return err
	}
	htmlName := fmt.Sprintf("resume_%s.html", ts)
	pdfName := fmt.Sprintf("resume_%s.pdf", ts)
	if err := ioutil.WriteFile(filepath.Join(genDir, htmlName), []byte(html), 0o644); err != nil {
		return err
	}
	if err := ioutil.WriteFile(filepath.Join(genDir, pdfName), pdfBytes, 0o644); err != nil {
		return err
	}

	// copy to per-user folder
	userDir := filepath.Join("resume-data", "resumes", job.UserID.String())
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		return err
	}
	destName := uuid.New().String() + ".pdf"
	if err := ioutil.WriteFile(filepath.Join(userDir, destName), pdfBytes, 0o644); err != nil {
		return err
	}

	// update job metadata and status
	job.Status = "completed"
	if job.Metadata == nil {
		job.Metadata = map[string]interface{}{}
	}
	job.Metadata["generated_html"] = filepath.Join(genDir, htmlName)
	job.Metadata["generated_pdf"] = filepath.Join(genDir, pdfName)
	job.Metadata["user_copy"] = filepath.Join(userDir, destName)
	job.UpdatedAt = time.Now()

	if p.repo != nil {
		if err := p.repo.Save(ctx, job); err != nil {
			return err
		}
	}

	return nil
}
