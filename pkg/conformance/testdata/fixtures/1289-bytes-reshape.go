package main

import (
	"bytes"
	"fmt"
)

func main() {
	fmt.Println(string(bytes.ToUpper([]byte("Hello"))), string(bytes.ToLower([]byte("Hello"))))
	fmt.Println(string(bytes.TrimSpace([]byte("\t spaced \n"))))
	fmt.Println(string(bytes.Repeat([]byte("na"), 4)))
	parts := bytes.Split([]byte("1-2-3-4"), []byte("-"))
	fmt.Println(len(parts))
	fmt.Println(string(bytes.Join(parts, []byte(", "))))
}
