columns-3:
@for col in nums | columns(3) {
- {{ col | join(",") }}
@}
columns-2-fill:
@for col in nums | columns(2, "_") {
- {{ col | join(",") }}
@}
entries:
@for pair in scores | entries {
{{ pair[0] }}={{ pair[1] }}
@}
sort-by-key:
@for k, v in scores | sort_map(by: "key") {
{{ k }}={{ v }}
@}
sort-by-value:
@for k, v in scores | sort_map(by: "value") {
{{ k }}={{ v }}
@}
selectattr-truthy: {{ people | selectattr("active") | map(attribute: "name") | join(",") }}
rejectattr-truthy: {{ people | rejectattr("active") | map(attribute: "name") | join(",") }}
selectattr-test: {{ people | selectattr("age", "ge", 18) | map(attribute: "name") | join(",") }}
