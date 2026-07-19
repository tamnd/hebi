package main

import "fmt"

func main() {
	err := fmt.Errorf("code %d for %s", 404, "path")
	fmt.Println(err)
}
