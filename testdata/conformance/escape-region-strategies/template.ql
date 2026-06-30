off-default: {{ payload }}
@escape html {
html-region: {{ payload }}
@escape js {
js-nested: {{ payload }}
@escape css {
css-deepest: {{ payload }}
@}
js-again: {{ payload }}
@}
html-again: {{ payload }}
raw-site: {{ payload | raw }}
@}
off-default-again: {{ payload }}
@escape url {
url-region: {{ payload }}
@}
@escape html_attr {
attr-region: {{ payload }}
@escape html_attr_relaxed {
attr-relaxed-nested: {{ payload }}
@}
@}
