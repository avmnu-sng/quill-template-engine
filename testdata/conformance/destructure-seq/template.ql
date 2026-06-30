@set [a, b] = pair
@set [head, ...rest] = nums
@set [x, [y, z]] = nested
@set [first, {label}] = mixed
{{ a }} + {{ b }}
{{ head }} :: {{ rest|json }}
{{ x }} / {{ y }} / {{ z }}
{{ first }} = {{ label }}
