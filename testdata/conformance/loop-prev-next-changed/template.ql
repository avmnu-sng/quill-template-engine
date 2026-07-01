prev/next over a sequence:
@for n in nums {
{{ n }}: prev={{ loop.prev ?? "-" }} next={{ loop.next ?? "-" }}
@}
prev/next over a mapping (values):
@for k, v in meta {
{{ k }}: prev={{ loop.prev ?? "-" }} next={{ loop.next ?? "-" }}
@}
changed section headers:
@for row in rows {
@if loop.changed(row.group) {
[{{ row.group }}]
@}
  - {{ row.name }}
@}
