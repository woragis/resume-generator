package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"os"
	"path/filepath"
)

func main() {
	in := "profile_override.json"
	b, err := ioutil.ReadFile(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read profile: %v\n", err)
		os.Exit(2)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal: %v\n", err)
		os.Exit(2)
	}
	profile, _ := m["profile"].(map[string]interface{})
	tplPath := filepath.Join("templates", "template.html")
	tpl, err := template.ParseFiles(tplPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse tpl: %v\n", err)
		os.Exit(2)
	}
	data := map[string]interface{}{"Profile": profile}
	var outFile = filepath.Join("resume-data", "generated", "resume_test_links.html")
	f, err := os.Create(outFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create out: %v\n", err)
		os.Exit(2)
	}
	defer f.Close()
	if err := tpl.Execute(f, data); err != nil {
		fmt.Fprintf(os.Stderr, "execute tpl: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("wrote %s\n", outFile)
}
