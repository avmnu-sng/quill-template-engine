// Module quillbench is Quill's benchmark harness. It is a SEPARATE nested module
// so the engine's own module stays standard-library-only: the third-party peer
// engines (pongo2, stick, jet, quicktemplate) are dependencies of this harness
// alone, and even those are gated behind the "thirdparty" build tag so the
// default Quill-vs-stdlib comparison builds and runs offline with zero external
// dependencies. The quicktemplate render functions are qtc-generated into the
// quillbench/qtpl subpackage, whose committed *.qtpl.go also carries the
// "thirdparty" tag so the default build never pulls quicktemplate.
//
// The replace directive points at the parent engine source tree (this module
// lives at <repo>/bench, so the parent is one level up). Nothing else is
// required by the default build.
module quillbench

go 1.26.2

require (
	github.com/CloudyKit/jet/v6 v6.2.0
	github.com/avmnu-sng/quill-template-engine v0.0.0
	github.com/flosch/pongo2/v6 v6.1.0
	github.com/tyler-sommer/stick v1.0.10
	github.com/valyala/quicktemplate v1.8.0
)

require (
	github.com/CloudyKit/fastprinter v0.0.0-20200109182630-33d98a066a53 // indirect
	github.com/shopspring/decimal v1.3.1 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
)

replace github.com/avmnu-sng/quill-template-engine => ../
