upper: {{ name | upper }}
lower: {{ name | lower }}
trim: [{{ padded | trim }}]
default: {{ missing | default("fallback") }}
length: {{ items | length }}
join: {{ items | join(", ") }}
sort-join: {{ items | sort | join("-") }}
reverse: {{ items | reverse | join(",") }}
first-last: {{ items | first }}..{{ items | last }}
replace: {{ name | replace({"a": "4"}) }}
chain: {{ name | upper | replace({"A": "@"}) }}
