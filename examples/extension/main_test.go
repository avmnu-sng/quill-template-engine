package main

func Example() {
	if err := render(); err != nil {
		panic(err)
	}
	// Output:
	// ababab
	// 5 -> 5
	// -3 -> 0
	// 42 -> 10
}
