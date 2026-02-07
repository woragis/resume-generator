package usecase

import (
	"context"
	"fmt"
	"resume-generator/internal/model"
	ai "resume-generator/pkg/ai"
)

// StageValidationResult holds validation state for a stage
type StageValidationResult struct {
	Valid      bool
	Missing    []string
	PartialMap map[string]interface{}
	Error      string
}

// Stage1Validator validates Foundation stage: meta.name, meta.headline, meta.contact
func Stage1Validator(resumeMap map[string]interface{}) *StageValidationResult {
	result := &StageValidationResult{
		Valid:      true,
		Missing:    []string{},
		PartialMap: map[string]interface{}{},
	}

	// Extract meta
	metaRaw, hasMeta := resumeMap["meta"]
	if !hasMeta {
		result.Valid = false
		result.Missing = append(result.Missing, "meta")
		return result
	}

	meta, ok := metaRaw.(map[string]interface{})
	if !ok {
		result.Valid = false
		result.Error = "meta is not a map"
		return result
	}

	// Check required meta fields
	if name, ok := meta["name"].(string); !ok || name == "" {
		result.Valid = false
		result.Missing = append(result.Missing, "meta.name")
	}

	if headline, ok := meta["headline"].(string); !ok || headline == "" {
		result.Valid = false
		result.Missing = append(result.Missing, "meta.headline")
	}

	if contact, ok := meta["contact"].(map[string]interface{}); !ok || len(contact) == 0 {
		result.Valid = false
		result.Missing = append(result.Missing, "meta.contact")
	}

	result.PartialMap = meta
	return result
}

// Stage2Validator validates Professional History: experience[], role, company, bullets
func Stage2Validator(resumeMap map[string]interface{}) *StageValidationResult {
	result := &StageValidationResult{
		Valid:      true,
		Missing:    []string{},
		PartialMap: map[string]interface{}{},
	}

	expRaw, hasExp := resumeMap["experience"]
	if !hasExp {
		result.Valid = false
		result.Missing = append(result.Missing, "experience")
		return result
	}

	expArr, ok := expRaw.([]interface{})
	if !ok || len(expArr) == 0 {
		result.Valid = false
		result.Error = fmt.Sprintf("experience is invalid type/empty: %T", expRaw)
		return result
	}

	// Validate each experience entry has required fields
	for i, exp := range expArr {
		expMap, ok := exp.(map[string]interface{})
		if !ok {
			result.Valid = false
			result.Missing = append(result.Missing, fmt.Sprintf("experience[%d] invalid type", i))
			continue
		}

		if role, ok := expMap["role"].(string); !ok || role == "" {
			result.Valid = false
			result.Missing = append(result.Missing, fmt.Sprintf("experience[%d].role", i))
		}

		if company, ok := expMap["company"].(string); !ok || company == "" {
			result.Valid = false
			result.Missing = append(result.Missing, fmt.Sprintf("experience[%d].company", i))
		}

		if bullets, ok := expMap["bullets"].([]interface{}); !ok || len(bullets) == 0 {
			result.Valid = false
			result.Missing = append(result.Missing, fmt.Sprintf("experience[%d].bullets", i))
		}
	}

	result.PartialMap = map[string]interface{}{"experience": expArr}
	return result
}

// Stage3Validator validates Showcase Content: projects[], publications[], certifications[]
func Stage3Validator(resumeMap map[string]interface{}) *StageValidationResult {
	result := &StageValidationResult{
		Valid:      true,
		Missing:    []string{},
		PartialMap: map[string]interface{}{},
	}

	// Check projects
	projRaw, hasProj := resumeMap["projects"]
	if !hasProj {
		result.Valid = false
		result.Missing = append(result.Missing, "projects")
	} else if projArr, ok := projRaw.([]interface{}); !ok || len(projArr) == 0 {
		result.Valid = false
		result.Missing = append(result.Missing, "projects (empty or invalid)")
	} else {
		result.PartialMap["projects"] = projArr
	}

	// Check publications
	pubsRaw, hasPubs := resumeMap["publications"]
	if !hasPubs {
		result.Valid = false
		result.Missing = append(result.Missing, "publications")
	} else if pubsArr, ok := pubsRaw.([]interface{}); !ok || len(pubsArr) == 0 {
		result.Valid = false
		result.Missing = append(result.Missing, "publications (empty or invalid)")
	} else {
		result.PartialMap["publications"] = pubsArr
	}

	// Check certifications
	certsRaw, hasCerts := resumeMap["certifications"]
	if !hasCerts {
		result.Valid = false
		result.Missing = append(result.Missing, "certifications")
	} else if certsArr, ok := certsRaw.([]interface{}); !ok || len(certsArr) == 0 {
		result.Valid = false
		result.Missing = append(result.Missing, "certifications (empty or invalid)")
	} else {
		result.PartialMap["certifications"] = certsArr
	}

	return result
}

// Stage4Validator validates Synthesis: summary, extras[], final meta polish
func Stage4Validator(resumeMap map[string]interface{}) *StageValidationResult {
	result := &StageValidationResult{
		Valid:      true,
		Missing:    []string{},
		PartialMap: map[string]interface{}{},
	}

	// Check summary
	if sumRaw, has := resumeMap["summary"]; !has {
		result.Valid = false
		result.Missing = append(result.Missing, "summary")
	} else if sum, ok := sumRaw.(string); !ok || sum == "" || len(sum) < 80 || len(sum) > 330 {
		result.Valid = false
		result.Missing = append(result.Missing, fmt.Sprintf("summary (invalid length: %d)", len(sum)))
	} else {
		result.PartialMap["summary"] = sum
	}

	// Check extras
	extrasRaw, hasExtras := resumeMap["extras"]
	if !hasExtras {
		result.Valid = false
		result.Missing = append(result.Missing, "extras")
	} else if extrasArr, ok := extrasRaw.([]interface{}); !ok || len(extrasArr) == 0 {
		result.Valid = false
		result.Missing = append(result.Missing, "extras (empty or invalid)")
	} else {
		result.PartialMap["extras"] = extrasArr
	}

	return result
}

// Stage1Enrich attempts to generate missing meta fields
func Stage1Enrich(ctx context.Context, aiClient *ai.Client, payload map[string]interface{}, resumeMap map[string]interface{}, validation *StageValidationResult) error {
	if validation.Valid {
		return nil
	}

	fmt.Printf("processor: Stage 1 enriching: %v\n", validation.Missing)

	// Call AI to generate meta
	out, err := aiClient.FormatProfileSnapshot(ctx, payload)
	if err != nil {
		fmt.Printf("processor: Stage1Enrich FormatProfileSnapshot failed: %v\n", err)
		return err
	}

	if out == nil {
		return fmt.Errorf("Stage1Enrich: no output from FormatProfileSnapshot")
	}

	// Validate against schema
	if err := model.ValidateMapWithSchema("templates/schema/profile.schema.json", out); err != nil {
		fmt.Printf("processor: Stage1Enrich validation failed: %v, attempting EnrichFields\n", err)
		
		// Try targeted enrichment
		fields, err := aiClient.EnrichFields(ctx, map[string]interface{}{
			"meta": out["meta"],
		})
		if err == nil && fields != nil {
			out = fields
		} else {
			// Fallback to broad enrichment
			enriched, err := aiClient.EnrichResume(ctx, resumeMap, out)
			if err == nil && enriched != nil {
				out = enriched
			} else {
				return fmt.Errorf("Stage1Enrich: enrichment failed: %v", err)
			}
		}
	}

	// Merge meta into resumeMap
	if meta, ok := out["meta"].(map[string]interface{}); ok {
		resumeMap["meta"] = meta
	}

	// Validate again
	revalidation := Stage1Validator(resumeMap)
	if !revalidation.Valid {
		return fmt.Errorf("Stage1Enrich: still invalid after enrichment: %v", revalidation.Missing)
	}

	return nil
}

// Stage2Enrich attempts to generate missing experience fields
func Stage2Enrich(ctx context.Context, aiClient *ai.Client, payload map[string]interface{}, resumeMap map[string]interface{}, validation *StageValidationResult) error {
	if validation.Valid {
		return nil
	}

	fmt.Printf("processor: Stage 2 enriching: %v\n", validation.Missing)

	// Call AI to generate experience
	out, err := aiClient.FormatExperienceProjects(ctx, payload)
	if err != nil {
		fmt.Printf("processor: Stage2Enrich FormatExperienceProjects failed: %v\n", err)
		return err
	}

	if out == nil {
		return fmt.Errorf("Stage2Enrich: no output from FormatExperienceProjects")
	}

	// Validate against schema
	if err := model.ValidateMapWithSchema("templates/schema/experience.schema.json", out); err != nil {
		fmt.Printf("processor: Stage2Enrich validation failed: %v, attempting enrichment\n", err)
		
		// Fallback to broad enrichment with context
		enriched, err := aiClient.EnrichResume(ctx, resumeMap, out)
		if err != nil {
			return fmt.Errorf("Stage2Enrich: enrichment failed: %v", err)
		}
		out = enriched
	}

	// Merge experience into resumeMap
	if exp, ok := out["experience"].([]interface{}); ok {
		resumeMap["experience"] = exp
	}

	// Validate again
	revalidation := Stage2Validator(resumeMap)
	if !revalidation.Valid {
		return fmt.Errorf("Stage2Enrich: still invalid after enrichment: %v", revalidation.Missing)
	}

	return nil
}

// Stage3Enrich attempts to generate missing showcase content
func Stage3Enrich(ctx context.Context, aiClient *ai.Client, payload map[string]interface{}, resumeMap map[string]interface{}, validation *StageValidationResult) error {
	if validation.Valid {
		return nil
	}

	fmt.Printf("processor: Stage 3 enriching: %v\n", validation.Missing)

	// Call AI to generate showcase content
	out, err := aiClient.FormatPublicationsCertsExtras(ctx, payload)
	if err != nil {
		fmt.Printf("processor: Stage3Enrich FormatPublicationsCertsExtras failed: %v\n", err)
		return err
	}

	if out == nil {
		return fmt.Errorf("Stage3Enrich: no output from FormatPublicationsCertsExtras")
	}

	// Validate against schema
	if err := model.ValidateMapWithSchema("templates/schema/publications.schema.json", out); err != nil {
		fmt.Printf("processor: Stage3Enrich validation failed: %v, attempting enrichment\n", err)
		
		// Fallback to broad enrichment
		enriched, err := aiClient.EnrichResume(ctx, resumeMap, out)
		if err != nil {
			return fmt.Errorf("Stage3Enrich: enrichment failed: %v", err)
		}
		out = enriched
	}

	// Merge into resumeMap
	for _, key := range []string{"projects", "publications", "certifications"} {
		if val, ok := out[key]; ok {
			resumeMap[key] = val
		}
	}

	// Validate again
	revalidation := Stage3Validator(resumeMap)
	if !revalidation.Valid {
		return fmt.Errorf("Stage3Enrich: still invalid after enrichment: %v", revalidation.Missing)
	}

	return nil
}

// Stage4Enrich attempts to generate missing synthesis content
func Stage4Enrich(ctx context.Context, aiClient *ai.Client, payload map[string]interface{}, resumeMap map[string]interface{}, validation *StageValidationResult) error {
	if validation.Valid {
		return nil
	}

	fmt.Printf("processor: Stage 4 enriching: %v\n", validation.Missing)

	// Build assembled payload with validated stages
	assembled := map[string]interface{}{
		"assembled": resumeMap,
		"aggregated": payload["aggregated"],
	}

	// Call AI to generate summary and polish meta
	out, err := aiClient.FormatSummaryMeta(ctx, assembled)
	if err != nil {
		fmt.Printf("processor: Stage4Enrich FormatSummaryMeta failed: %v\n", err)
		return err
	}

	if out == nil {
		return fmt.Errorf("Stage4Enrich: no output from FormatSummaryMeta")
	}

	// Merge summary
	if sum, ok := out["summary"].(string); ok {
		if len(sum) >= 80 && len(sum) <= 330 {
			resumeMap["summary"] = sum
		} else {
			return fmt.Errorf("Stage4Enrich: summary length invalid: %d", len(sum))
		}
	}

	// Merge extras
	if extras, ok := out["extras"].([]interface{}); ok {
		resumeMap["extras"] = extras
	}

	// Merge meta fields (preserve existing name/headline)
	if metaRaw, ok := out["meta"].(map[string]interface{}); ok {
		metaObj := map[string]interface{}{}
		if m, ok := resumeMap["meta"].(map[string]interface{}); ok {
			for k, v := range m {
				metaObj[k] = v
			}
		}
		for k, v := range metaRaw {
			if k != "name" && k != "headline" {
				metaObj[k] = v
			}
		}
		resumeMap["meta"] = metaObj
	}

	// Validate again
	revalidation := Stage4Validator(resumeMap)
	if !revalidation.Valid {
		return fmt.Errorf("Stage4Enrich: still invalid after enrichment: %v", revalidation.Missing)
	}

	return nil
}
