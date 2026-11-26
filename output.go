package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Tarektouati/made/internal/lint"
	"github.com/Tarektouati/made/internal/task"
)

// listedTask is the public --list --json shape. Scripts are intentionally omitted.
type listedTask struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Language    string `json:"language"`
	Line        int    `json:"line"`
	Group       string `json:"group,omitempty"`
}

func printTasksJSON(w io.Writer, tasks []task.Task) error {
	listed := make([]listedTask, len(tasks))
	for i, t := range tasks {
		listed[i] = listedTask{t.Name, t.Description, t.Lang, t.Line, t.Group}
	}
	return json.NewEncoder(w).Encode(listed)
}

type jsonDiagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

func printCheckDiagnostics(w io.Writer, path string, diags []lint.Diagnostic, format string) (errs, warns int, err error) {
	if format == "text" {
		errs, warns = printDiagnostics(w, path, diags)
		return errs, warns, nil
	}
	for _, d := range diags {
		if d.Severity == lint.Warning {
			warns++
		} else {
			errs++
		}
	}
	switch format {
	case "json":
		out := make([]jsonDiagnostic, len(diags))
		for i, d := range diags {
			out[i] = jsonDiagnostic{path, d.Line, d.Severity.String(), d.Rule, d.Msg}
		}
		err = json.NewEncoder(w).Encode(out)
	case "github":
		for _, d := range diags {
			fmt.Fprintf(w, "::%s file=%s", d.Severity, escapeAnnotationProperty(path))
			if d.Line > 0 {
				fmt.Fprintf(w, ",line=%d", d.Line)
			}
			fmt.Fprintf(w, ",title=%s::%s\n", escapeAnnotationProperty(d.Rule), escapeAnnotationData(d.Msg))
		}
	}
	return errs, warns, err
}

// The workflow command protocol escapes data and properties differently. Escape
// percent signs first so literal escape sequences survive one round of decoding.
func escapeAnnotationData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func escapeAnnotationProperty(s string) string {
	return strings.NewReplacer(":", "%3A", ",", "%2C").Replace(escapeAnnotationData(s))
}
