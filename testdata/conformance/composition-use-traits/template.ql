@use "buttons.ql" with { cancel: dismiss }
own header
@block submit {
{{ parent() }}
own submit wins
@}
{{ block("dismiss") }}
