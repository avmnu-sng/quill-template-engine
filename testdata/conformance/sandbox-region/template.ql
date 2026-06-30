before: {{ msg | lower }}
@sandbox {
inside-for:
@for n in nums {
- {{ n }} sq={{ n ** 2 }}
@}
@if nums | length > 1 {
many ({{ nums | length }})
@}
upper: {{ msg | upper }}
@}
after: {{ msg | lower }}
