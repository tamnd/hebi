package main

import "fmt"

func main() {
	done := false
	finish := func() {
		done = true
	}
	fmt.Println(done)
	finish()
	fmt.Println(done)
}
