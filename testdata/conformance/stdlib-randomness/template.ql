random-int: {{ random(1000) }}
random-range: {{ random(10, 20) }}
random-element: {{ random(items) }}
shuffle: {{ items | shuffle | join(",") }}
shuffle-seeded-arg: {{ items | shuffle(42) | join(",") }}
