package main

import (
	"fmt"
	"unicode/utf8"
)

func main() {
	s := "Hello, 世界!"
	fmt.Println(utf8.RuneCountInString(s), len(s), utf8.RuneCount([]byte(s)))
	fmt.Println(utf8.ValidString(s), utf8.Valid([]byte(s)))
	fmt.Println(utf8.ValidString(string([]byte{0x80})), utf8.ValidString(string([]byte{0xe4, 0xb8})))
	fmt.Println(utf8.RuneLen('A'), utf8.RuneLen('é'), utf8.RuneLen('世'), utf8.RuneLen(0x1F680))
	fmt.Println(utf8.ValidRune('A'), utf8.ValidRune(0xD800), utf8.ValidRune(utf8.MaxRune))
	fmt.Println(utf8.RuneError, utf8.RuneSelf, utf8.MaxRune, utf8.UTFMax)
}
