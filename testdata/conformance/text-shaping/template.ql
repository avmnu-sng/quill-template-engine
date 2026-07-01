wrap: [{{ "the quick brown fox" | wrap(9) }}]
wrap-break: [{{ "a b c d" | wrap(3, "|") }}]
truncate: [{{ "hello world foo" | truncate(11) }}]
truncate-word: [{{ "hello world foo" | truncate(11, "...", true) }}]
truncate-short: [{{ "short" | truncate(10) }}]
center: [{{ "hi" | center(6) }}]
center-odd: [{{ "hi" | center(5) }}]
center-fill: [{{ "x" | center(5, "*") }}]
wordcount: {{ "  the  quick brown " | wordcount }}
