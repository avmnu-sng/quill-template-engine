len: {{ len(items) }}
keys: {{ keys(config) | join(",") }}
max: {{ max(3, 9, 1) }}
min: {{ min([4, 2, 8]) }}
attribute: {{ attribute(config, "host") }}
cycle: {{ cycle(labels, 0) }} {{ cycle(labels, 1) }} {{ cycle(labels, 2) }} {{ cycle(labels, 3) }}
date: {{ date(ts, "UTC") | date("2006-01-02") }}
date_modify: {{ "2021-03-04" | date_modify("+2 days") | date("2006-01-02") }}
random-seeded: {{ random(1000, 99) }}
