package cli

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/lukashankeln/glint/internal/rules"
	"github.com/lukashankeln/glint/internal/version"
)

type summaryJSON struct {
	Summary    summaryCounts    `json:"summary"`
	Violations []violationJSON  `json:"violations"`
}

type summaryCounts struct {
	Total    int `json:"total"`
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Info     int `json:"info"`
}

type violationJSON struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	File     string `json:"file,omitempty"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	NS       string `json:"namespace,omitempty"`
}

func buildSummary(violations []rules.Violation) summaryJSON {
	s := summaryJSON{
		Violations: make([]violationJSON, 0, len(violations)),
	}
	for _, v := range violations {
		s.Summary.Total++
		switch v.Severity {
		case rules.SeverityError:
			s.Summary.Errors++
		case rules.SeverityWarning:
			s.Summary.Warnings++
		case rules.SeverityInfo:
			s.Summary.Info++
		}
		s.Violations = append(s.Violations, violationJSON{
			RuleID:   v.RuleID,
			Severity: string(v.Severity),
			Message:  v.Message,
			File:     v.FilePath,
			Kind:     v.ResourceKind,
			Name:     v.ResourceName,
			NS:       v.ResourceNS,
		})
	}
	return s
}

func writeOutput(w io.Writer, violations []rules.Violation, format string) error {
	switch strings.ToLower(format) {
	case "github-actions":
		return writeGitHubActions(w, violations)
	case "sarif":
		return writeSARIF(w, violations)
	case "json":
		s := buildSummary(violations)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	case "junit":
		return writeJUnit(w, violations)
	default: // "text" or ""
		return writeText(w, violations)
	}
}

func writeText(w io.Writer, violations []rules.Violation) error {
	for _, v := range violations {
		source := v.Source
		if source == "" {
			source = "cel"
		}

		var location string
		if v.ResourceKind == "" && v.ResourceName == "" {
			// No resource context — use the file path directly.
			location = v.FilePath
		} else {
			ns := v.ResourceNS
			resource := v.ResourceKind + "/" + v.ResourceName
			if ns != "" {
				resource = v.ResourceKind + "/" + ns + "/" + v.ResourceName
			}
			location = resource + " (" + v.APIVersion + ")"
		}

		_, err := fmt.Fprintf(w, "[%s] %s  %s: %s  [%s]\n",
			strings.ToUpper(string(v.Severity)),
			source,
			location,
			v.Message,
			v.RuleID,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func writeGitHubActions(w io.Writer, violations []rules.Violation) error {
	for _, v := range violations {
		level := "notice"
		switch v.Severity {
		case rules.SeverityError:
			level = "error"
		case rules.SeverityWarning:
			level = "warning"
		}

		file := v.FilePath

		var location string
		if v.ResourceKind == "" && v.ResourceName == "" {
			location = file
		} else {
			resource := v.ResourceKind + "/" + v.ResourceName
			if v.ResourceNS != "" {
				resource = v.ResourceKind + "/" + v.ResourceNS + "/" + v.ResourceName
			}
			location = resource + " (" + v.APIVersion + ")"
		}

		// Escape GitHub annotation message characters.
		raw := fmt.Sprintf("%s: %s  [%s]", location, v.Message, v.RuleID)
		msg := strings.ReplaceAll(raw, "%", "%25")
		msg = strings.ReplaceAll(msg, "\r", "%0D")
		msg = strings.ReplaceAll(msg, "\n", "%0A")
		title := fmt.Sprintf("%s [%s]", location, v.RuleID)
		title = strings.ReplaceAll(title, ",", "%2C")
		title = strings.ReplaceAll(title, ":", "%3A")

		var line string
		if file != "" {
			line = fmt.Sprintf("::%s file=%s,title=%s::%s", level, file, title, msg)
		} else {
			line = fmt.Sprintf("::%s title=%s::%s", level, title, msg)
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// sarifLevel maps glint severity to SARIF notification level.
func sarifLevel(sev rules.Severity) string {
	switch sev {
	case rules.SeverityError:
		return "error"
	case rules.SeverityWarning:
		return "warning"
	default:
		return "note"
	}
}

func writeSARIF(w io.Writer, violations []rules.Violation) error {
	// Collect unique rules for the driver rule list.
	rulesSeen := map[string]bool{}
	type sarifRule struct {
		ID               string            `json:"id"`
		ShortDescription map[string]string `json:"shortDescription"`
		DefaultConfig    map[string]string `json:"defaultConfiguration"`
	}
	sarifRules := []sarifRule{}

	type sarifLocation struct {
		PhysicalLocation map[string]any `json:"physicalLocation"`
	}
	type sarifResult struct {
		RuleID    string          `json:"ruleId"`
		Level     string          `json:"level"`
		Message   map[string]string `json:"message"`
		Locations []sarifLocation `json:"locations,omitempty"`
	}

	results := make([]sarifResult, 0, len(violations))

	for _, v := range violations {
		if !rulesSeen[v.RuleID] {
			rulesSeen[v.RuleID] = true
			sarifRules = append(sarifRules, sarifRule{
				ID:               v.RuleID,
				ShortDescription: map[string]string{"text": v.RuleID},
				DefaultConfig:    map[string]string{"level": sarifLevel(v.Severity)},
			})
		}

		r := sarifResult{
			RuleID:  v.RuleID,
			Level:   sarifLevel(v.Severity),
			Message: map[string]string{"text": v.Message},
		}
		if v.FilePath != "" {
			r.Locations = []sarifLocation{
				{
					PhysicalLocation: map[string]any{
						"artifactLocation": map[string]string{
							"uri": v.FilePath,
						},
					},
				},
			}
		}
		results = append(results, r)
	}

	out := map[string]any{
		"$schema": "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json",
		"version": "2.1.0",
		"runs": []map[string]any{
			{
				"tool": map[string]any{
					"driver": map[string]any{
						"name":            "glint",
						"version":         version.Version,
						"informationUri":  "https://github.com/lukashankeln/glint",
						"rules":           sarifRules,
					},
				},
				"results": results,
			},
		},
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeJUnit(w io.Writer, violations []rules.Violation) error {
	type junitFailure struct {
		XMLName xml.Name `xml:"failure"`
		Message string   `xml:"message,attr"`
		Type    string   `xml:"type,attr"`
		Body    string   `xml:",chardata"`
	}
	type junitCase struct {
		XMLName   xml.Name      `xml:"testcase"`
		Name      string        `xml:"name,attr"`
		Classname string        `xml:"classname,attr"`
		Failure   *junitFailure `xml:",omitempty"`
	}
	type junitSuite struct {
		XMLName  xml.Name    `xml:"testsuite"`
		Name     string      `xml:"name,attr"`
		Tests    int         `xml:"tests,attr"`
		Failures int         `xml:"failures,attr"`
		Cases    []junitCase `xml:"testcase"`
	}
	type junitSuites struct {
		XMLName xml.Name     `xml:"testsuites"`
		Suites  []junitSuite `xml:"testsuite"`
	}

	failures := 0
	cases := make([]junitCase, 0, len(violations))
	for _, v := range violations {
		name := v.RuleID
		if v.FilePath != "" {
			name = v.RuleID + ": " + v.FilePath
		}
		classname := v.FilePath
		if classname == "" {
			classname = "glint"
		}
		body := fmt.Sprintf("[%s] %s", strings.ToUpper(string(v.Severity)), v.Message)
		if v.ResourceKind != "" {
			body += fmt.Sprintf(" (%s/%s)", v.ResourceKind, v.ResourceName)
		}
		cases = append(cases, junitCase{
			Name:      name,
			Classname: classname,
			Failure: &junitFailure{
				Message: v.Message,
				Type:    string(v.Severity),
				Body:    body,
			},
		})
		failures++
	}

	out := junitSuites{
		Suites: []junitSuite{
			{
				Name:     "glint",
				Tests:    len(cases),
				Failures: failures,
				Cases:    cases,
			},
		},
	}

	if _, err := fmt.Fprint(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	return enc.Flush()
}
