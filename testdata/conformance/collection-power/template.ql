map-attr: {{ people | map(attribute: "name") | join(",") }}
map-dotted: {{ people | map(attribute: "role.title") | join(",") }}
sum: {{ nums | sum }}
sum-attr: {{ people | sum(attribute: "age") }}
sum-float: {{ prices | sum }}
unique: {{ dups | unique | join(",") }}
unique-attr: {{ people | unique(attribute: "role.title") | map(attribute: "name") | join(",") }}
select-even: {{ nums | select("even") | join(",") }}
reject-even: {{ nums | reject("even") | join(",") }}
select-ge: {{ nums | select("ge", 3) | join(",") }}
selectattr: {{ people | selectattr("age", "ge", 18) | map(attribute: "name") | join(",") }}
rejectattr: {{ people | rejectattr("age", "ge", 18) | map(attribute: "name") | join(",") }}
selectattr-eq: {{ people | selectattr("role.title", "eq", "eng") | map(attribute: "name") | join(",") }}
eq: {{ 3 is eq(3) }} {{ 3 is eq(4) }}
ne: {{ 3 is ne(4) }}
lt-le: {{ 3 is lt(4) }} {{ 4 is le(4) }}
gt-ge: {{ 5 is gt(4) }} {{ 4 is ge(4) }}
group-by-path:
@set groups = people | group_by("role.title")
@for g in groups {
  {{ g.key }} -> {{ g.items | map(attribute: "name") | join("+") }}
@}
group-by-arrow: {{ people | group_by(p => p.role.title) | map(attribute: "key") | join(",") }}
