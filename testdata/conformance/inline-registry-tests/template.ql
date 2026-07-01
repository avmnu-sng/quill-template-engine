upper-is-filter:{{ "upper" is filter }}
missing-is-filter:{{ "nope" is filter }}
range-is-function:{{ "range" is function }}
missing-is-function:{{ "nope" is function }}
empty-is-test:{{ "empty" is test }}
missing-is-test:{{ "nope" is test }}
not-filter:{{ "nope" is not filter }}
cross-kind:{{ "upper" is function }}
@set name = "lower"
var-subject:{{ name is filter }}
@if "join" is filter {
guard-yes:join is a filter
@} else {
guard-no:join is missing
@}
