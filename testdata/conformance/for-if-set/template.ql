@set total = 0
@for n in nums {
@set total = total + n
{{ loop.index }}: {{ n }} (running {{ total }})
@}
total = {{ total }}
@if total > 10 {
big
@} elseif total > 0 {
small
@} else {
zero
@}
@for k, v in meta {
{{ k }} -> {{ v }}
@}
@for x in empties {
{{ x }}
@} else {
no items
@}
