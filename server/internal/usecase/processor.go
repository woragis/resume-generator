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
			// If a job_application_id was provided on the job, fetch that
			// specific job application and include it in the aggregated payload
			if job.Metadata != nil {
				if jaidRaw, ok := job.Metadata["job_application_id"]; ok {
					if jaid, ok2 := jaidRaw.(string); ok2 && jaid != "" {
						if ja, err := repo.GetJobApplicationByID(ctx, jaid); err == nil {
							// ensure agg is a map-like structure
							if ar, ok := aggregated.(repo.AggregateResult); ok {
								ar["job_application"] = ja
								aggregated = ar
							}
						} else {
							fmt.Printf("processor: failed to fetch job_application %s: %v\n", jaid, err)
						}
					}
				}
			}
			// merge aggregated data with any provided profile overrides
			// preprocess overrides so publications/certifications meet schema
			var overrides *Overrides
			if job.Profile != nil {
				overrides = NewOverridesFromMap(job.Profile)
			} else {
				overrides = &Overrides{Other: map[string]interface{}{}}
			}

			// overrides is already normalized by NewOverridesFromMap

			// overrides is already normalized by NewOverridesFromMap

			payload := map[string]interface{}{
				"aggregated": agg,
				"overrides":  overrides.ToMap(),
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

		resumeMap := map[string]interface{}{}
		var warnings []string
		synthesized := false
		var baseResume map[string]interface{}

		// Optional split AI flow: enabled by default unless explicitly
		// disabled via AI_SPLIT_FLOW=false. When enabled we perform focused
		// calls per-section to reduce schema drift. Otherwise fall back to
		// the original single-call FormatResume.
		if os.Getenv("AI_SPLIT_FLOW") != "false" {
			// prepare payload containing aggregated and overrides
			payload := map[string]interface{}{}
			if m, ok := rawForAI.(map[string]interface{}); ok {
				payload = m
			} else {
				payload["aggregated"] = rawForAI
			}

			// A: experience + projects
			if p.aiClient != nil {
				if out, e := p.aiClient.FormatExperienceProjects(ctx, payload); e == nil && out != nil {
					// validate against small schema
					if err := model.ValidateMapWithSchema("templates/schema/experience.schema.json", out); err != nil {
						fmt.Printf("processor: experience/projects validation failed: %v\n", err)
						// attempt focused enrichment via EnrichResume to fix experience/projects
						if p.aiClient != nil {
							overrides := map[string]interface{}{}
							if ex, ok := out["experience"]; ok { overrides["experience"] = ex }
							if pr, ok := out["projects"]; ok { overrides["projects"] = pr }
							if len(overrides) > 0 {
								if enriched, enErr := p.aiClient.EnrichResume(ctx, resumeMap, overrides); enErr == nil && enriched != nil {
									tmp := map[string]interface{}{}
									if ex, ok := enriched["experience"]; ok { tmp["experience"] = ex }
									if pr, ok := enriched["projects"]; ok { tmp["projects"] = pr }
									if err2 := model.ValidateMapWithSchema("templates/schema/experience.schema.json", tmp); err2 == nil {
										for k, v := range tmp { resumeMap[k] = v }
									} else {
										fmt.Printf("processor: experience enrichment still invalid: %v\n", err2)
									}
								} else {
									fmt.Printf("processor: EnrichResume for experience failed: %v\n", enErr)
								}
							}
						}
					} else {
						for k, v := range out {
							resumeMap[k] = v
						}
					}
				} else if e != nil {
					fmt.Printf("processor: FormatExperienceProjects failed: %v\n", e)
				}
				// B: profile + snapshot
				if out, e := p.aiClient.FormatProfileSnapshot(ctx, payload); e == nil && out != nil {
					// validate profile snapshot small schema
					if err := model.ValidateMapWithSchema("templates/schema/profile.schema.json", out); err != nil {
						fmt.Printf("processor: profile/snapshot validation failed: %v\n", err)
						// attempt focused enrichment first (EnrichFields) for snapshot/meta
						if p.aiClient != nil {
							overrides := map[string]interface{}{}
							if ss, ok := out["snapshot"]; ok { overrides["snapshot"] = ss }
							if mm, ok := out["meta"]; ok { overrides["meta"] = mm }
							if len(overrides) > 0 {
								// try targeted EnrichFields which returns only the requested keys
								if fields, ferr := p.aiClient.EnrichFields(ctx, overrides); ferr == nil && fields != nil {
									// merge returned fields into out
									if ss, ok := fields["snapshot"]; ok { out["snapshot"] = ss }
									if mm, ok := fields["meta"]; ok { out["meta"] = mm }
									if err2 := model.ValidateMapWithSchema("templates/schema/profile.schema.json", out); err2 == nil {
										for k, v := range out { resumeMap[k] = v }
										continue
									} else {
										fmt.Printf("processor: profile EnrichFields still invalid: %v\n", err2)
									}
								}
								// fallback: broad EnrichResume
								if enriched, enErr := p.aiClient.EnrichResume(ctx, resumeMap, overrides); enErr == nil && enriched != nil {
									if ss, ok := enriched["snapshot"]; ok { out["snapshot"] = ss }
									if mm, ok := enriched["meta"]; ok { out["meta"] = mm }
									if err2 := model.ValidateMapWithSchema("templates/schema/profile.schema.json", out); err2 == nil {
										for k, v := range out { resumeMap[k] = v }
									} else {
										fmt.Printf("processor: profile enrichment still invalid: %v\n", err2)
									}
								} else {
									fmt.Printf("processor: EnrichFields/EnrichResume for profile failed: %v\n", enErr)
								}
							}
						}
					} else {
						for k, v := range out {
							resumeMap[k] = v
						}
					}
				} else if e != nil {
					fmt.Printf("processor: FormatProfileSnapshot failed: %v\n", e)
				}
				// C: publications + certifications + extras
				if out, e := p.aiClient.FormatPublicationsCertsExtras(ctx, payload); e == nil && out != nil {
					if err := model.ValidateMapWithSchema("templates/schema/publications.schema.json", out); err != nil {
						fmt.Printf("processor: publications/certs/extras validation failed: %v\n", err)
						// try focused enrichment via EnrichFields (targeted) or EnrichResume fallback
						if p.aiClient != nil {
							if fields, ferr := p.aiClient.EnrichFields(ctx, out); ferr == nil && fields != nil {
								// validate enriched fields merged into a tmp map
								tmp := map[string]interface{}{"publications": fields["publications"], "certifications": fields["certifications"], "extras": fields["extras"]}
								if err2 := model.ValidateMapWithSchema("templates/schema/publications.schema.json", tmp); err2 == nil {
									for k, v := range tmp { resumeMap[k] = v }
								} else {
									fmt.Printf("processor: publications enrichment still invalid: %v\n", err2)
								}
							} else {
								// fallback to broad enrichment
								if enriched, enErr := p.aiClient.EnrichResume(ctx, resumeMap, map[string]interface{}{"publications": out["publications"], "certifications": out["certifications"], "extras": out["extras"]}); enErr == nil && enriched != nil {
									tmp := map[string]interface{}{}
									if pubs, ok := enriched["publications"]; ok { tmp["publications"] = pubs }
									if certs, ok := enriched["certifications"]; ok { tmp["certifications"] = certs }
									if extras, ok := enriched["extras"]; ok { tmp["extras"] = extras }
									if err2 := model.ValidateMapWithSchema("templates/schema/publications.schema.json", tmp); err2 == nil {
										for k, v := range tmp { resumeMap[k] = v }
									} else {
										fmt.Printf("processor: publications broad enrichment still invalid: %v\n", err2)
									}
								} else {
									fmt.Printf("processor: EnrichFields/EnrichResume for publications failed: %v\n", ferr)
								}
							}
						}
					} else {
						for k, v := range out {
							resumeMap[k] = v
						}
					}
				} else if e != nil {
					fmt.Printf("processor: FormatPublicationsCertsExtras failed: %v\n", e)
				}
				// D: summary + meta polish (final harmonization)
				// assemble a lightweight assembled payload to give context
				assembled := map[string]interface{}{"assembled": resumeMap, "aggregated": payload["aggregated"]}
				if out, e := p.aiClient.FormatSummaryMeta(ctx, assembled); e == nil && out != nil {
					// handle summary (respect length) and merge meta rather than overwrite
					if s, ok := out["summary"].(string); ok {
						if len(s) < 80 || len(s) > 220 {
							fmt.Printf("processor: summary length out of bounds: %d\n", len(s))
						} else {
							resumeMap["summary"] = s
						}
					}
					// merge meta fields into existing meta, preserving name if present
					if metaRaw, ok := out["meta"].(map[string]interface{}); ok {
						metaObj := map[string]interface{}{}
						if m, ok2 := resumeMap["meta"].(map[string]interface{}); ok2 {
							for k, v := range m { metaObj[k] = v }
						}
						for k, v := range metaRaw {
							if k == "name" {
								if _, has := metaObj["name"]; !has || metaObj["name"] == "" {
									metaObj["name"] = v
								}
							} else {
								metaObj[k] = v
							}
						}
						resumeMap["meta"] = metaObj
					}
				} else if e != nil {
					fmt.Printf("processor: FormatSummaryMeta failed: %v\n", e)
				}
			}
			// keep baseResume as a snapshot for targeted merges later
			baseResume = map[string]interface{}{}
			for k, v := range resumeMap {
				baseResume[k] = v
			}
			} else {
				resumeMap, warnings, synthesized, err = p.aiClient.FormatResume(ctx, rawForAI)
				if err != nil {
					return err
				}
				// Keep a copy of the base resume returned from the first AI call.
				baseResume = map[string]interface{}{}
				for k, v := range resumeMap {
					baseResume[k] = v
				}
			}

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
							// convert AI-provided fields into typed Overrides for safer handling
							// fields is already a map[string]interface{} from the AI client
							fieldsMap := fields
							ofields := NewOverridesFromMap(fieldsMap)
							// merge into copy of baseResume
							merged := map[string]interface{}{}
							for k, v := range baseResume {
								merged[k] = v
							}
                            
							// merge typed fields into the resume map
							if len(ofields.Publications) > 0 {
								pubs := make([]interface{}, 0, len(ofields.Publications))
								for _, s := range ofields.Publications {
									pubs = append(pubs, s)
								}
								merged["publications"] = pubs
							}
							if len(ofields.Certifications) > 0 {
								certs := make([]interface{}, 0, len(ofields.Certifications))
								for _, c := range ofields.Certifications {
									m := map[string]interface{}{"name": c.Name}
									if c.Issuer != "" {
										m["issuer"] = c.Issuer
									}
									if c.Date != "" {
										m["date"] = c.Date
									}
									if c.URL != "" {
										m["url"] = c.URL
									}
									if c.Description != "" {
										m["description"] = c.Description
									}
									certs = append(certs, m)
								}
								merged["certifications"] = certs
							}
							if len(ofields.Extras) > 0 {
								extras := make([]interface{}, 0, len(ofields.Extras))
								for _, e := range ofields.Extras {
									extras = append(extras, map[string]interface{}{"category": e.Category, "text": e.Text})
								}
								merged["extras"] = extras
							}

							// ensure publications meet minLength
							if arr, ok := merged["publications"].([]interface{}); ok {
								for i, it := range arr {
									if s, ok := it.(string); ok {
										// leave short publications as-is; we'll ask the AI to
										// expand them via EnrichFields instead of synthesizing here.
										_ = s
									} else {
										arr[i] = fmt.Sprintf("%v", it)
									}
								}
								merged["publications"] = arr
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
							// Leave short publications as-is; EnrichFields will
							// be used to expand them via AI rather than synthesizing
							// text locally here.
							_ = s
						} else {
							arr[i] = fmt.Sprintf("%v", it)
						}
					}
					m["publications"] = arr
				}
			}

			// certifications -> []interface{} of objects
			if c, ok := m["certifications"]; ok {
				out := []interface{}{}
				switch t := c.(type) {
				case []interface{}:
					for _, it := range t {
						switch v := it.(type) {
						case string:
							out = append(out, map[string]interface{}{"name": v})
						case map[string]interface{}:
							out = append(out, v)
						default:
							out = append(out, map[string]interface{}{"name": fmt.Sprintf("%v", v)})
						}
					}
				case string:
					out = append(out, map[string]interface{}{"name": t})
				default:
					out = append(out, map[string]interface{}{"name": fmt.Sprintf("%v", t)})
				}
				m["certifications"] = out
			}

			// extras -> []interface{} of objects {category, text}
			if e, ok := m["extras"]; ok {
				out := []interface{}{}
				switch t := e.(type) {
				case string:
					s := strings.TrimSpace(t)
					if len(s) > 140 {
						s = s[:140]
					}
					out = append(out, map[string]interface{}{"category": "misc", "text": s})
				case []interface{}:
					for _, it := range t {
						switch v := it.(type) {
						case string:
							s := strings.TrimSpace(v)
							if len(s) > 140 {
								s = s[:140]
							}
							out = append(out, map[string]interface{}{"category": "misc", "text": s})
						case map[string]interface{}:
							cat := "misc"
							if c, ok := v["category"].(string); ok && c != "" {
								cat = c
							}
							txt := ""
							if s, ok := v["text"].(string); ok {
								txt = s
								if len(txt) > 140 {
									txt = txt[:140]
								}
							}
							out = append(out, map[string]interface{}{"category": cat, "text": txt})
						default:
							out = append(out, map[string]interface{}{"category": "misc", "text": fmt.Sprintf("%v", v)})
						}
					}
				default:
					out = append(out, map[string]interface{}{"category": "misc", "text": fmt.Sprintf("%v", t)})
				}
				m["extras"] = out
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

		// HARD-MERGE: ensure meta and social_links are present from aggregated
		// If the AI omitted meta.social_links, copy from the first aggregated
		// profile (aggregator.go already normalizes profile.social_links).
		if aggregated != nil {
			if aggMap, ok := aggregated.(repo.AggregateResult); ok {
				// find first profile's social_links if present
				var profileMeta map[string]interface{}
				if pRaw, ok := aggMap["profiles"]; ok {
					switch parr := pRaw.(type) {
					case []interface{}:
						if len(parr) > 0 {
							if first, ok := parr[0].(map[string]interface{}); ok {
								// profile might contain a nested `meta` or flat fields
								if m, ok := first["meta"].(map[string]interface{}); ok {
									profileMeta = m
								} else {
									profileMeta = map[string]interface{}{}
									// copy some common fields if present (include social_links)
									for _, k := range []string{"name", "headline", "contact", "website", "bio", "social_links"} {
										if v, ok := first[k]; ok {
											profileMeta[k] = v
										}
									}
								}
							}
						}
					}
				}
				if profileMeta != nil {
					// ensure resumeMap.meta exists
					metaObj := map[string]interface{}{}
					if m, ok := resumeMap["meta"].(map[string]interface{}); ok {
						metaObj = m
					}
					// copy missing name/headline/contact
					if name, ok := profileMeta["name"].(string); ok {
						if _, has := metaObj["name"]; !has || metaObj["name"] == "" {
							metaObj["name"] = name
						}
					}
					if head, ok := profileMeta["headline"].(string); ok {
						if _, has := metaObj["headline"]; !has || metaObj["headline"] == "" {
							metaObj["headline"] = head
						}
					}
					if c, ok := profileMeta["contact"].(map[string]interface{}); ok {
						if _, has := metaObj["contact"]; !has || metaObj["contact"] == nil {
							metaObj["contact"] = c
						}
					}
					// ensure social_links
					if sl, ok := profileMeta["social_links"]; ok {
						has := false
						if msl, ok2 := metaObj["social_links"]; ok2 {
							// treat empty object as missing
							if mm, ok3 := msl.(map[string]interface{}); ok3 {
								if len(mm) > 0 {
									has = true
								}
							}
						}
						if !has {
							metaObj["social_links"] = sl
						}
					}
					resumeMap["meta"] = metaObj
				}
			}
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

		// All per-experience summaries must be produced by the AI.
		// The processor no longer synthesizes role summaries locally; if the
		// AI omitted summaries, we will attempt a focused EnrichFields call
		// to request the missing fields instead of fabricating them here.
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

	// save HTML artifact before rendering so it's preserved even if rendering fails
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

	// produce PDF with retry and validation
	var pdfBytes []byte
	var renderErr error
	attempts := 3
	for i := 0; i < attempts; i++ {
		pdfBytes, renderErr = p.renderer.RenderHTMLToPDF(ctx, html)
		if renderErr == nil {
			// validate basic PDF signature
			if len(pdfBytes) > 0 && strings.HasPrefix(string(pdfBytes), "%PDF") {
				renderErr = nil
				break
			}
			renderErr = fmt.Errorf("invalid PDF output (len=%d)", len(pdfBytes))
		}
		fmt.Printf("processor: render attempt %d failed: %v\n", i+1, renderErr)
		// exponential backoff before retrying
		if i < attempts-1 {
			backoff := time.Duration(1<<i) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	if renderErr != nil {
		// log and continue; preserve HTML and record metadata
		fmt.Printf("processor: rendering failed after %d attempts: %v\n", attempts, renderErr)
	} else {
		if err := ioutil.WriteFile(filepath.Join(genDir, pdfName), pdfBytes, 0o644); err != nil {
			return err
		}
	}

	// copy to per-user folder
	userDir := filepath.Join("resume-data", "resumes", job.UserID.String())
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		return err
	}
	// copy PDF to per-user folder if rendering succeeded
	if renderErr == nil && len(pdfBytes) > 0 {
		destName := uuid.New().String() + ".pdf"
		if err := ioutil.WriteFile(filepath.Join(userDir, destName), pdfBytes, 0o644); err != nil {
			return err
		}
		job.Metadata["user_copy"] = filepath.Join(userDir, destName)
	} else {
		job.Metadata["user_copy"] = ""
		job.Metadata["pdf_render_error"] = fmt.Sprintf("render failed: %v", renderErr)
	}

	// update job metadata and status
	job.Status = "completed"
	if job.Metadata == nil {
		job.Metadata = map[string]interface{}{}
	}
	job.Metadata["generated_html"] = filepath.Join(genDir, htmlName)
	if renderErr == nil && len(pdfBytes) > 0 {
		job.Metadata["generated_pdf"] = filepath.Join(genDir, pdfName)
	} else {
		job.Metadata["generated_pdf"] = ""
	}
	job.UpdatedAt = time.Now()

	if p.repo != nil {
		if err := p.repo.Save(ctx, job); err != nil {
			return err
		}
	}

	return nil
}
