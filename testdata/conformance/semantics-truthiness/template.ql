zero-int: {{ 0 ? "t" : "f" }}
one-int: {{ 1 ? "t" : "f" }}
zero-string: {{ "0" ? "t" : "f" }}
empty-string: {{ "" ? "t" : "f" }}
space-string: {{ " " ? "t" : "f" }}
empty-array: {{ [] ? "t" : "f" }}
nonempty-array: {{ [0] ? "t" : "f" }}
null-value: {{ null ? "t" : "f" }}
float-zero: {{ 0.0 ? "t" : "f" }}
is-empty: {{ [] is empty ? "y" : "n" }}{{ "" is empty ? "y" : "n" }}{{ 0 is empty ? "y" : "n" }}
