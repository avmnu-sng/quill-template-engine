divisible_by-yes: {{ 10 is divisible_by(5) }}
divisible_by-no: {{ 10 is divisible_by(3) }}
divisible-by-twoword: {{ 9 is divisible by(3) }}
sequence-list: {{ items is sequence }}
sequence-map: {{ config is sequence }}
mapping-map: {{ config is mapping }}
mapping-list: {{ items is mapping }}
true-bool: {{ flag is true }}
true-int: {{ 1 is true }}
not-true: {{ flag is not true }}
constant-yes: {{ pi is constant("PI") }}
constant-no: {{ 2.71 is constant("PI") }}
