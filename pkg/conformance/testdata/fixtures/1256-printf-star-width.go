package main

import "fmt"

func main() {
	fmt.Printf("[%*d] [%.*f] [%-*d|]\n", 6, 7, 2, 3.14159, 5, 3)
}
