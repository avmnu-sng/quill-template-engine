@types {
  title: string
  count: int
  tags: list<string>
  scores: map<string, int>
@}
@set shown: int = count + 1
{{ title | upper }} ({{ shown }})
@for tag in tags {
- {{ tag }}
@}
@for k, v in scores {
{{ k }}={{ v }}
@}
