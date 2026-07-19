package main

import "fmt"

func clamp(n int) (out int) {
	if n < 0 {
		out = 0
		return
	}
	out = n
	return
}

func main() {
	fmt.Println(clamp(-5), clamp(9))
}
