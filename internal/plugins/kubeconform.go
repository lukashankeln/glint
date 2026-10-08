package plugins

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lukashankeln/glint/internal/rules"
)

// kubeconformOutput is the top-level object emitted by kubeconform -output json.
type kubeconformOutput struct {
	Resources []kubeconformResource `json:"resources"`
}

type kubeconformResource struct {
	Kind             string             `json:"kind"`
	Name             string             `json:"name"`
	Namespace        string             `json:"namespace"`
	Status           string             `json:"status"`
	Msg              string             `json:"msg"`
	ValidationErrors []kubeconformError `json:"validationErrors"`
}

type kubeconformError struct {
	Path string `json:"path"`
	Msg  string `json:"msg"`
}

// parseKubeconform translates kubeconform's JSON output into glint violations.
// Resources with status "statusValid" or "statusSkipped" are discarded.
// Resources with status "statusInvalid" or "statusError" become violations —
// one per validation error entry, falling back to the top-level msg when
// validationErrors is empty.
func parseKubeconform(stdout []byte, severityOverride rules.Severity) ([]rules.Violation, error) {
	var out kubeconformOutput
	if err := json.Unmarshal(stdout, &out); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	sev := rules.SeverityError
	if severityOverride != "" {
		sev = severityOverride
	}

	var violations []rules.Violation
	for _, r := range out.Resources {
		if r.Status == "statusValid" || r.Status == "statusSkipped" {
			continue
		}
		if len(r.ValidationErrors) > 0 {
			for _, ve := range r.ValidationErrors {
				msg := ve.Msg
				if ve.Path != "" {
					msg = fmt.Sprintf("%s: %s", ve.Path, ve.Msg)
				}
				violations = append(violations, rules.Violation{
					RuleID:       "kubeconform",
					Severity:     sev,
					Message:      msg,
					Source:       "kubeconform",
					ResourceKind: r.Kind,
					ResourceName: r.Name,
					ResourceNS:   r.Namespace,
				})
			}
		} else {
			violations = append(violations, rules.Violation{
				RuleID:       "kubeconform",
				Severity:     sev,
				Message:      strings.TrimSpace(r.Msg),
				Source:       "kubeconform",
				ResourceKind: r.Kind,
				ResourceName: r.Name,
				ResourceNS:   r.Namespace,
			})
		}
	}
	return violations, nil
}
