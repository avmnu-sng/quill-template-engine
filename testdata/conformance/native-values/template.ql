name: {{ user.name | upper }}
tags: {{ user.tags | join("/") }}
city: {{ user.addr.city }}
@for k, v in user.meta {
  {{ k }}={{ v }}
@}
scores: {{ scores | join(",") }}
