Symbol table collected from many sites, emitted once in the file shell.

imports:
@yield imports
symbols:
@yield symbols

@provide imports {import "os"
@}
@provide imports {import "fmt"
@}
@for f in funcs {
@provide symbols {func {{ f.name }}({{ f.arity }})
@}
@}
@provide imports {import "strings"
@}
