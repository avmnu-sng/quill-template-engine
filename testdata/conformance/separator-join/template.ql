@set sep = separator(", ")
items:
@for n in nums {~
{{- sep() }}{{ n -}}
@}

@set d = separator()
default:
@for n in [1, 2] {~
{{- d() }}{{ n -}}
@}
