package main

func Example() {
	if err := render(); err != nil {
		panic(err)
	}
	// Output:
	// roster (3):
	// 1. ADA * <ada@example.com>
	// 2. BOB <no-email>
	// 3. CLEO <cleo@example.com>
	// sorted: ada, bob, cleo
}
