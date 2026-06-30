default-html: {{ snippet }}
@escape off {
off-region: {{ snippet }}
@escape html {
html-nested: {{ snippet }}
@}
off-again: {{ snippet }}
@}
default-html-again: {{ snippet }}
@escape js {
js-region: {{ snippet }}
@}
back-to-default: {{ snippet }}
