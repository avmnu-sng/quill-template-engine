@set [a, b?] = shortPair
@set [c, d?] = fullPair
@set [, second] = leadElide
@set [first, , third] = midElide
@set [head, opt?, ...rest] = withTail
@set [x, [y, z]?, ...more] = nestedFull
@set [p, [q, r]?, ...tail] = nestedShort
a={{ a }} b={{ b }}
c={{ c }} d={{ d }}
second={{ second }}
first={{ first }} third={{ third }}
head={{ head }} opt={{ opt }} rest={{ rest|json }}
x={{ x }} y={{ y }} z={{ z }} more={{ more|json }}
p={{ p }} q={{ q }} r={{ r }} tail={{ tail|json }}
