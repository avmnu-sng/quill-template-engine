@macro field(name, **opts) {
{{ name }}[{{ opts | keys | join(",") }}]
@}
@macro attr(name, **opts) {
{{ name }}={{ opts.class ?? "-" }}
@}
@macro tag(name, kind, **attrs) {
{{ kind }}:{{ name }}:{{ attrs.role ?? "-" }}
@}
@macro row(label, ...cells, **attrs) {
{{ label }}|{{ cells | join(",") }}|{{ attrs.id ?? "-" }}
@}
@macro forward(**opts) {
{{ field("x", ...opts) }}
@}
collect:{{ field("email", id: "e1", class: "big") }}
empty:{{ field("plain") }}
read:{{ attr("input", class: "wide") }}
positional:{{ tag("a", "link", role: "nav") }}
variadic:{{ row("r", 1, 2, 3, id: "x") }}
spread:{{ forward(class: "fwd") }}
