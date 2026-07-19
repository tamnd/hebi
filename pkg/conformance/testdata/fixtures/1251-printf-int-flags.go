package main

import "fmt"

func main() {
	fmt.Printf("[%d] [%5d] [%-5d] [%05d] [%+d] [% d]\n", 42, 42, 42, 42, 42, 42)
	fmt.Printf("[%.3d] [%6.3d] [%d] [%05d]\n", 42, 42, -42, -42)
}
