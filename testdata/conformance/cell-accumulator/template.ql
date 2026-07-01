@set acc = cell(0)
@for w in weights {
@set acc.value = acc.value + w
@}
sum: {{ acc.value }}
@set words = cell("")
@set join = separator(" ")
@for label in labels {
@set words.value = words.value ~ join() ~ label
@}
joined: {{ words.value }}
