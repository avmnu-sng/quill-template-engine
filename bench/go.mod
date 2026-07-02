// Module quillbench is Quill's benchmark harness. It is a SEPARATE nested module
// so the engine's own module stays standard-library-only: the third-party peer
// engines (pongo2, stick) are dependencies of this harness alone, and even those
// are gated behind the "thirdparty" build tag so the default Quill-vs-stdlib
// comparison builds and runs offline with zero external dependencies.
//
// The replace directive points at the parent engine source tree (this module
// lives at <repo>/bench, so the parent is one level up). Nothing else is
// required by the default build.
module quillbench

go 1.26.2

require github.com/avmnu-sng/quill-template-engine v0.0.0

replace github.com/avmnu-sng/quill-template-engine => ../
