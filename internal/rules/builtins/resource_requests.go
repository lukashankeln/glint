package builtins

import (
	"fmt"
	"strings"
)

// resourceRequestsExpr regenerates the resource-requests expression. With
// init_containers: false, only regular containers must carry requests — init
// containers often come from third-party charts that do not expose their
// resources, and they rarely drive scheduling (the effective pod request is
// the max of any init container and the sum of the regular containers).
func resourceRequestsExpr(params map[string]any) string {
	includeInit := true
	if v, ok := params["init_containers"].(bool); ok {
		includeInit = v
	}
	return ResourceRequestsExpression(includeInit)
}

// ResourceRequestsExpression builds the resource-requests CEL expression.
// ResourceRequestsExpression(true) is the default (init containers included).
func ResourceRequestsExpression(includeInit bool) string {
	podSpecs := []string{
		"resource.spec",
		"resource.spec.template.spec",
		"resource.spec.jobTemplate.spec.template.spec",
	}
	guards := []string{
		"resource.spec.containers",
		"resource.spec.template",
		"resource.spec.jobTemplate",
	}
	var clauses []string
	for i, spec := range podSpecs {
		check := allHaveRequests(spec + ".containers")
		if includeInit {
			init := spec + ".initContainers"
			check = fmt.Sprintf("%s &&\n    (!has(%s) ||\n     %s == null ||\n     %s)",
				check, init, init, allHaveRequests(init))
		}
		clauses = append(clauses, fmt.Sprintf("(\n  !has(%s) ||\n  (\n    %s\n  )\n)", guards[i], check))
	}
	return strings.Join(clauses, " &&\n") + "\n"
}

func allHaveRequests(list string) string {
	return list + ".all(c,\n      has(c.resources) && has(c.resources.requests) &&\n" +
		"      has(c.resources.requests.cpu) && has(c.resources.requests.memory))"
}
