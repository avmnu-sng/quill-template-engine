@from "forms.ql" import field
@import "forms.ql" as forms
@macro greet(who) {
Hello, {{ who }}.
@}
{{ greet("world") }}
{{ field("email") }}
{{ field("pw", "Password") }}
{{ forms.list(1, 2, 3) }}
