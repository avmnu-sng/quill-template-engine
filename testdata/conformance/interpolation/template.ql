name: {{ user.name }}
greeting: {{ "Hello, #{user.name}!" }}
sum: {{ 1 + 2 * 3 }}
concat: {{ "v" ~ 1 ~ "." ~ 0 }}
index: {{ user.roles[0] }}
slice: {{ user.name[0:3] }}
coalesce: {{ user.nickname ?? "(none)" }}
ternary: {{ user.active ? "on" : "off" }}
