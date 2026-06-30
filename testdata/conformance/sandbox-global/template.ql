@set top = max(scores)
top: {{ top }}
@for i in 1..3 {
row {{ i }}: {{ labels | join("-") | upper }}
@}
