@macro field(name, label = null) {
{{ label ?? name }}: <{{ name }}>
@}
@macro list(...items) {
{{ items | join(" | ") }}
@}
