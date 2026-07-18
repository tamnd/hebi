package main

import "fmt"

func main() {
	seen := map[int]bool{}
	seen[3] = true
	fmt.Println(seen[3], seen[4])
}
