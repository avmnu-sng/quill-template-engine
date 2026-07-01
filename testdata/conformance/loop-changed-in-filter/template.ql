top-level dedup of adjacent duplicates:
@for x in samples if loop.changed(x) {
{{ loop.index }}: {{ x }}
@}
filter changed over a derived key:
@for n in nums if loop.changed(n >= 10) {
{{ n }}
@}
nested filter answers for the inner loop, enclosing changed stays intact:
@for g in groups {
group {{ g.name }} first-here={{ loop.changed(g.name) }}
@for tag in g.tags if loop.changed(tag) {
  - {{ tag }}
@}
@}
