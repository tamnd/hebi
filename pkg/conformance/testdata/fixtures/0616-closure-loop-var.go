package main

import "fmt"

func main() {
	funcs := []func() int{}
	for i := 0; i < 3; i++ {
		funcs = append(funcs, func() int {
			return i
		})
	}
	for i := 0; i < 3; i++ {
		f := funcs[i]
		fmt.Println(f())
	}
}
