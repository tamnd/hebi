package build

import "testing"

// asserts shares a Speaker interface satisfied by a value-receiver method and a
// couple of concrete types used by the type assertion programs below.
const asserts = "package main\n\nimport \"fmt\"\n\n" +
	"type Speaker interface {\n\tSpeak() string\n}\n\n" +
	"type Named struct {\n\tName string\n}\n\n" +
	"func (n Named) Speak() string {\n\treturn n.Name\n}\n\n" +
	"type Counter struct {\n\tN int\n}\n\n"

// TestTypeAssert checks the type assertion surface against go run for the forms
// that return without panicking. The one-result form returns the concrete value
// when the interface holds the asserted type, the comma-ok form reports a hit with
// the value and a miss with the target's zero value and false, a concrete target
// matches only its exact dynamic type so a bool never satisfies an int assertion,
// an interface target matches structurally through the value's methods, a struct
// value target extracts an independent copy the caller may mutate without touching
// the interface, a pointer target reaches the stored pointer, a nil interface
// misses every target, and a deferred recover swallows a failed one-result
// assertion so the program returns normally.
func TestTypeAssert(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"one-result assertion returns the concrete value",
			asserts + "func main() {\n\tvar i interface{} = 7\n\tn := i.(int)\n\tfmt.Println(n + 1)\n}\n",
		},
		{
			"comma-ok reports a hit with the value",
			asserts + "func main() {\n\tvar i interface{} = \"hi\"\n\ts, ok := i.(string)\n\tfmt.Println(ok, s)\n}\n",
		},
		{
			"comma-ok reports a miss with the zero value and false",
			asserts + "func main() {\n\tvar i interface{} = 7\n\ts, ok := i.(string)\n\tfmt.Println(ok, len(s))\n}\n",
		},
		{
			"a bool does not satisfy an int assertion",
			asserts + "func main() {\n\tvar i interface{} = true\n\tn, ok := i.(int)\n\tfmt.Println(ok, n)\n}\n",
		},
		{
			"an int does satisfy an int assertion but not a bool one",
			asserts + "func main() {\n\tvar i interface{} = 5\n\tb, ok := i.(bool)\n\tn, ok2 := i.(int)\n\tfmt.Println(ok, b, ok2, n)\n}\n",
		},
		{
			"an interface target matches structurally",
			asserts + "func main() {\n\tvar i interface{} = Named{\"Rex\"}\n\ts, ok := i.(Speaker)\n\tfmt.Println(ok, s.Speak())\n}\n",
		},
		{
			"an interface target misses a value that lacks the method",
			asserts + "func main() {\n\tvar i interface{} = 7\n\ts, ok := i.(Speaker)\n\tfmt.Println(ok, s == nil)\n}\n",
		},
		{
			"a struct value target extracts an independent copy",
			asserts + "func main() {\n\tvar i interface{} = Counter{5}\n\tc := i.(Counter)\n\tc.N = 99\n\tfmt.Println(i.(Counter).N, c.N)\n}\n",
		},
		{
			"a pointer target reaches the stored pointer",
			asserts + "func main() {\n\tvar i interface{} = &Counter{3}\n\tp, ok := i.(*Counter)\n\tp.N = 8\n\tfmt.Println(ok, p.N)\n}\n",
		},
		{
			"a nil interface misses every target",
			asserts + "func main() {\n\tvar i interface{}\n\tn, ok := i.(int)\n\ts, ok2 := i.(Speaker)\n\tfmt.Println(ok, n, ok2, s == nil)\n}\n",
		},
		{
			"a deferred recover swallows a failed one-result assertion",
			asserts + "func main() {\n\tvar i interface{} = 7\n\tdefer func() {\n\t\tr := recover()\n\t\tfmt.Println(\"recovered\", r != nil)\n\t}()\n\t_ = i.(string)\n\tfmt.Println(\"unreachable\")\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestTypeAssertCrash checks that an unrecovered failed assertion crashes the way
// go run does, exiting with status two and printing the same panic banner. A
// concrete target reports the dynamic type it held against the target it wanted,
// and an interface target reports the first method the value is missing.
func TestTypeAssertCrash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"a failed concrete assertion crashes",
			asserts + "func main() {\n\tvar i interface{} = 7\n\tfmt.Println(\"before\")\n\t_ = i.(string)\n\tfmt.Println(\"after\")\n}\n",
		},
		{
			"a failed interface assertion names the missing method",
			asserts + "func main() {\n\tvar i interface{} = 7\n\tfmt.Println(\"before\")\n\t_ = i.(Speaker)\n\tfmt.Println(\"after\")\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramCrashesLikeGo(t, tt.source)
		})
	}
}

// TestTypeAssertEmit pins the runtime calls the two forms lower to. The one-result
// form calls _type_assert with the value, the target class, whether the check is
// structural, the source and target names for a failed assertion's message, and
// the target interface's method names, and a struct value target clones the result.
// The comma-ok form calls _type_assert_ok with the target's zero value for a miss.
func TestTypeAssertEmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			"one-result concrete assertion",
			asserts + "func main() {\n\tvar i interface{} = 7\n\tn := i.(int)\n\tfmt.Println(n)\n}\n",
			"n = _hebirt._type_assert(i, int, False, b\"interface {}\", b\"int\", [])\n",
		},
		{
			"one-result struct value assertion clones",
			asserts + "func main() {\n\tvar i interface{} = Counter{5}\n\tc := i.(Counter)\n\tfmt.Println(c.N)\n}\n",
			"c = _hebirt._type_assert(i, Counter, False, b\"interface {}\", b\"main.Counter\", []).copy()\n",
		},
		{
			"comma-ok interface assertion",
			asserts + "func main() {\n\tvar i interface{} = Named{\"x\"}\n\ts, ok := i.(Speaker)\n\tfmt.Println(ok, s)\n}\n",
			"s, ok = _hebirt._type_assert_ok(i, Speaker, True, None)\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := emitOf(t, tt.source)
			if !bytesContains(got, tt.want) {
				t.Errorf("emitted main.py = \n%s\nwant it to contain\n%s", got, tt.want)
			}
		})
	}
}
