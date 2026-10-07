package builtins

// ParamSubstitutors maps built-in rule IDs to functions that regenerate the
// CEL expression from config params. Rules with a substitutor always use the
// generated expression — the YAML expression field is ignored at runtime.
var ParamSubstitutors = map[string]func(params map[string]any) string{
	"resource-requests": resourceRequestsExpr,
}
