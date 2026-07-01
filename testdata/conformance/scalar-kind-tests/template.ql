@set sep = separator(",")
string-str: {{ "x" is string }}
string-int: {{ 1 is string }}
number-int: {{ 1 is number }}
number-float: {{ 1.5 is number }}
number-str: {{ "1" is number }}
int-int: {{ 1 is int }}
int-float: {{ 1.5 is int }}
float-float: {{ 1.5 is float }}
float-int: {{ 1 is float }}
bool-true: {{ flag is bool }}
bool-int: {{ 1 is bool }}
callable-sep: {{ sep is callable }}
callable-str: {{ "x" is callable }}
not-callable: {{ 1 is not callable }}
