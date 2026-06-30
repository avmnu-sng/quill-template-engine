constant: {{ constant("PI") }}
constant-check-yes: {{ constant("PI", null, true) }}
constant-check-no: {{ constant("MISSING", null, true) }}
enum-first: {{ enum("Color") }}
enum-cases: {{ enum_cases("Color") | join(",") }}
