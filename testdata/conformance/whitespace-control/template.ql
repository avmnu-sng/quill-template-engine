items = [
@for x in xs {~
  {{ x }},
@}~
]
hard:a   {{- glue -}}   b
line:p  {{~ glue ~}}  q
keep:{{ glue }}
