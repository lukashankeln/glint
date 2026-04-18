package builtins

// ParamSubstitutors maps built-in rule IDs to functions that regenerate the
// CEL expression from config params. Currently no built-in rules use params.
var ParamSubstitutors = map[string]func(params map[string]any) string{}
