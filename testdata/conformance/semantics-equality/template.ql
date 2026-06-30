int-vs-string: {{ 1 == "1" ? "eq" : "ne" }}
int-vs-float: {{ 1 == 1.0 ? "eq" : "ne" }}
zero-vs-false: {{ 0 == false ? "eq" : "ne" }}
null-vs-false: {{ null == false ? "eq" : "ne" }}
empty-vs-null: {{ "" == null ? "eq" : "ne" }}
array-eq: {{ [1, 2] == [1, 2] ? "eq" : "ne" }}
array-order: {{ [1, 2] == [2, 1] ? "eq" : "ne" }}
same-as-int-float: {{ 1 is same as(1.0) ? "same" : "diff" }}
ordering: {{ 2 < 10 ? "yes" : "no" }}
string-order: {{ "apple" < "banana" ? "yes" : "no" }}
spaceship: {{ 3 <=> 7 }} {{ 7 <=> 3 }} {{ 5 <=> 5 }}
regex-matches: {{ "err-42" matches "^err-[0-9]+$" ? "match" : "nomatch" }}
regex-flags: {{ "WARN" matches "(?i)^warn$" ? "match" : "nomatch" }}
