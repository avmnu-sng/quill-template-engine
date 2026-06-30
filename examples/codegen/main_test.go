package main

func Example() {
	if err := render(); err != nil {
		panic(err)
	}
	// Output:
	// type User struct {
	// 	ID int64
	// 	Name string
	// 	Tags []string
	// }
}
