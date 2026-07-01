groups: {{ rows | group_by("k") | length }}
@set grouped = rows | group_by("k")
@for g in grouped {
{{ g.key }} -> {{ g.items | map(attribute: "tag") | join("+") }}
@}
