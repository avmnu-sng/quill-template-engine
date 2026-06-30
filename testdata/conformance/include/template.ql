table:
@for r in rows {
@include "row.ql" with { label: r.label, value: r.value }
@}
@include "missing.ql" ignore missing
done
