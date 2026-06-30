js: {{ payload | e("js") }}
css: {{ payload | e("css") }}
html_attr: {{ payload | e("html_attr") }}
html_attr_relaxed: {{ payload | e("html_attr_relaxed") }}
url: {{ payload | e("url") }}
html: {{ payload | escape("html") }}
