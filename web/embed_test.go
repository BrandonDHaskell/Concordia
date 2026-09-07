package web

import (
	"html/template"
	"io/fs"
	"testing"
)

func TestTemplatesParse(t *testing.T) {
	_, err := template.ParseFS(Templates, "templates/*.html")
	if err != nil {
		t.Fatalf("templates do not parse: %v", err)
	}
}

func TestStaticAssetsPresent(t *testing.T) {
	for _, name := range []string{"static/htmx.min.js", "static/app.css"} {
		if _, err := fs.Stat(Static, name); err != nil {
			t.Errorf("missing asset %s: %v", name, err)
		}
	}
}
