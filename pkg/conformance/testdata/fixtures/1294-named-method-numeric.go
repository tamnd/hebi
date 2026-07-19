package main

import "fmt"

type Celsius float64

func (c Celsius) Fahrenheit() float64 {
	return float64(c)*9/5 + 32
}

type Duration int64

func (d Duration) Seconds() int64 {
	return int64(d) / 1000
}

func (d Duration) String() string {
	return fmt.Sprintf("%dms", int64(d))
}

func main() {
	c := Celsius(100)
	fmt.Println(c.Fahrenheit())
	warmer := c + Celsius(10)
	fmt.Println(warmer, warmer.Fahrenheit())

	var d Duration = 2500
	fmt.Println(d, d.Seconds())
	total := d + 500
	fmt.Printf("%v %s %d\n", total, total, int64(total))
}
