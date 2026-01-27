package model

import (
	"fmt"
	"path/filepath"

	"github.com/xeipuuv/gojsonschema"
)

// ValidateMap validates a generic map against the resume.schema.json file.
func ValidateMap(m map[string]interface{}) error {
	// Use absolute canonical file:// path for the schema so loaders on all
	// platforms (including Windows) resolve file references correctly.
	abs, err := filepath.Abs("templates/resume.schema.json")
	if err != nil {
		return err
	}
	schemaPath := "file://" + filepath.ToSlash(abs)
	schemaLoader := gojsonschema.NewReferenceLoader(schemaPath)
	docLoader := gojsonschema.NewGoLoader(m)

	res, err := gojsonschema.Validate(schemaLoader, docLoader)
	if err != nil {
		return err
	}
	if res.Valid() {
		return nil
	}
	// collect errors
	msgs := ""
	for _, e := range res.Errors() {
		msgs += fmt.Sprintf("%s; ", e.String())
	}
	return fmt.Errorf("schema validation failed: %s", msgs)
}
