package build

import (
	"testing"
)

// TestStrconvFuncs checks the strconv package lowering against go run. Each
// program exercises a family of the mapped functions, including the (value,
// error) shape and the *strconv.NumError a bad parse returns, and the
// differential harness holds the compiled output byte for byte against Go's.
func TestStrconvFuncs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"atoi and itoa",
			`package main

import (
	"fmt"
	"strconv"
)

func main() {
	n, err := strconv.Atoi("42")
	fmt.Println(n, err)
	m, err := strconv.Atoi("-7")
	fmt.Println(m, err)
	fmt.Println(strconv.Itoa(1234), strconv.Itoa(-99))
}
`,
		},
		{
			"atoi errors carry the num error",
			`package main

import (
	"fmt"
	"strconv"
)

func main() {
	_, err := strconv.Atoi("abc")
	fmt.Println(err)
	_, err = strconv.Atoi("")
	fmt.Println(err)
	_, err = strconv.Atoi("99999999999999999999")
	fmt.Println(err)
}
`,
		},
		{
			"parse int and uint with base",
			`package main

import (
	"fmt"
	"strconv"
)

func main() {
	v, _ := strconv.ParseInt("ff", 16, 64)
	fmt.Println(v)
	v, _ = strconv.ParseInt("0x1f", 0, 64)
	fmt.Println(v)
	v, _ = strconv.ParseInt("-101", 2, 64)
	fmt.Println(v)
	u, _ := strconv.ParseUint("377", 8, 64)
	fmt.Println(u)
	_, err := strconv.ParseInt("z", 16, 64)
	fmt.Println(err)
}
`,
		},
		{
			"format int uint and bool",
			`package main

import (
	"fmt"
	"strconv"
)

func main() {
	fmt.Println(strconv.FormatInt(255, 16), strconv.FormatInt(-255, 2))
	fmt.Println(strconv.FormatUint(511, 8), strconv.FormatInt(1000000, 10))
	fmt.Println(strconv.FormatBool(true), strconv.FormatBool(false))
	b, err := strconv.ParseBool("T")
	fmt.Println(b, err)
	_, err = strconv.ParseBool("yes")
	fmt.Println(err)
}
`,
		},
		{
			"quote",
			`package main

import (
	"fmt"
	"strconv"
)

func main() {
	fmt.Println(strconv.Quote("hi\tthere\n\"x\""))
}
`,
		},
		{
			"parse and format float",
			`package main

import (
	"fmt"
	"strconv"
)

func main() {
	f, err := strconv.ParseFloat("3.14159", 64)
	fmt.Println(f, err)
	g, _ := strconv.ParseFloat("1e10", 64)
	fmt.Println(g)
	_, err = strconv.ParseFloat("nope", 64)
	fmt.Println(err)
	fmt.Println(strconv.FormatFloat(3.14159, 'f', 2, 64))
	fmt.Println(strconv.FormatFloat(1234.5, 'e', 3, 64))
	fmt.Println(strconv.FormatFloat(0.0001234, 'g', -1, 64))
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}
