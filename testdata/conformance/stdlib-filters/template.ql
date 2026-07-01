capitalize: {{ phrase | capitalize }}
title: {{ phrase | title }}
ucfirst: {{ "camelCase" | ucfirst }}
striptags: {{ html | striptags }}
split-join: {{ phrase | split(" ") | join("-") }}
format: {{ "%s has %d words" | format("phrase", 4) }}
abs: {{ neg | abs }}
round: {{ 2.567 | round(1) }}
format_number: {{ price | format_number(2) }}
json: {{ config | json }}
column: {{ rows | column("name") | join(",") }}
batch: {{ [1, 2, 3, 4, 5] | batch(2) | map(c => c | join("")) | join("|") }}
url_encode: {{ "a b&c" | url_encode }}
tab: [{{ 2 | tab }}]end
tab-neg: [{{ "x" | tab(1 - 2) }}]end
nl2br: {{ "a\nb" | nl2br }}
spaceless: {{ "<a> <b>  </b>" | spaceless }}
convert_encoding: {{ phrase | convert_encoding("UTF-8") }}
indent: [{{ "x\ny" | indent(1, "  ") }}]end
