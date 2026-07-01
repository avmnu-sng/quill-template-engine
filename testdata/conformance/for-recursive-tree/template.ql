@for node in tree recursive {
@tab(loop.depth0) {
- {{ node.name }} (depth {{ loop.depth }})
@}
{{ loop(node.children) }}
@}
