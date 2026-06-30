@types {
  user: map<string, string>
@}
present: {{ user.name | default("anon") }}
absent: {{ user.nick | default("anon") }}
defined present: {{ "yes" if user.name is defined }}
defined absent: {{ "no" if user.nick is not defined }}
