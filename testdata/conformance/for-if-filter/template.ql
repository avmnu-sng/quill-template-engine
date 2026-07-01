active users (comma-joined over survivors):
@for u in users if u.active {~
{{ u.name }}{{- ", " if not loop.last -}}
@}~

count and positions:
@for n in nums if n % 2 == 0 {
{{ loop.index }}/{{ loop.length }}: {{ n }} (last={{ loop.last }})
@}
no survivors falls to else:
@for n in nums if n > 100 {
{{ n }}
@} else {
none matched
@}
mapping filter on the value:
@for k, v in scores if v >= 60 {
{{ loop.index }}. {{ k }} = {{ v }}
@}
