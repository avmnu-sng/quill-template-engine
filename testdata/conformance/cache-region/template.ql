@set n = 0
@cache key="hdr" ttl=60 tags=["nav"] {
@set n = n + 1
header build #{{ n }}
@}
@cache key="hdr" {
@set n = n + 1
header build #{{ n }}
@}
after n = {{ n }}
