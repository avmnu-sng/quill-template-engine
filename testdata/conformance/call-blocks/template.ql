@macro section(title) {
## {{ title }}
{{ caller() }}
@}
@macro field(name, kind) {
- {{ name }}: {{ caller(kind) }}
@}
@call section("Overview") {
This section wraps its body with a header.
@}
@call(t) field("id", "int") {
a value of type {{ t }}
@}
@call(t) field("label", "string") {
a value of type {{ t }}
@}
