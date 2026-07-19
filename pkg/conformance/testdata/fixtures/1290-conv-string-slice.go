package main

import "fmt"

func main() {
	s := "héllo, 世界"
	b := []byte(s)
	fmt.Println(len(b), string(b))
	r := []rune(s)
	fmt.Println(len(r))
	fmt.Println(string(r))
	fmt.Println(string(rune(72)), string(rune(0x1F600)), string(rune(0x110000)))
}
