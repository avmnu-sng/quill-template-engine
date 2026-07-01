@for node in tree recursive if node.visible {
@tab(loop.depth0) {
- {{ node.name }}
@}
{{ loop(node.children) }}
@}
