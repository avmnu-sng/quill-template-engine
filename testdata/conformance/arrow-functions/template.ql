map: {{ nums | map(x => x * x) | join(",") }}
filter: {{ nums | filter(x => x % 2 == 1) | join(",") }}
reduce: {{ nums | reduce((acc, x) => acc + x, 0) }}
find: {{ nums | find(x => x > 3) }}
sort-len: {{ words | sort((a, b) => (a | length) <=> (b | length)) | join(",") }}
has-some: {{ nums has some (x => x > 4) }}
has-every: {{ nums has every (x => x > 0) }}
has-every-false: {{ nums has every (x => x > 2) }}
chain: {{ nums | filter(x => x > 2) | map(x => x * 10) | join("/") }}
