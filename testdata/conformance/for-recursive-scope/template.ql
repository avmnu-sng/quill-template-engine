@set marker = "outer"
@for node in tree recursive {
{{ node.name }}
@set marker = node.name
{{ loop(node.children) }}
@}
after: {{ marker }}
