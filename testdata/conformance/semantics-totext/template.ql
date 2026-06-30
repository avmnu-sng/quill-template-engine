int: {{ 42 }}
neg-int: {{ -7 }}
float-integral: {{ 3.0 }}
float-frac: {{ 3.5 }}
float-from-div: {{ 7 / 2 }}
int-from-div: {{ 6 / 3 }}
bool-true: {{ true }}
bool-false: {{ false }}
null-renders-empty: [{{ null }}]
string-verbatim: {{ "a\tb" }}
power-int: {{ 2 ** 10 }}
power-float: {{ 2 ** -1 }}
big-int: {{ 9223372036854775807 }}
join-numbers: {{ [1, 2, 3] | join("+") }}
