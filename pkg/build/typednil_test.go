package build

import "testing"

// typednil shares a *MyError that satisfies error through a pointer receiver, the
// setup for the typed-nil trap where a nil pointer stored in an interface makes the
// interface non-nil.
const typednil = "package main\n\nimport \"fmt\"\n\n" +
	"type MyError struct {\n\tCode int\n}\n\n" +
	"func (e *MyError) Error() string {\n\treturn \"boom\"\n}\n\n"

// TestTypedNil pins the typed-nil trap against go run. A nil *MyError stored in an
// error, whether by return, by assignment, or by explicit conversion, is a non-nil
// interface because it carries a dynamic type, so it compares unequal to bare nil,
// while an interface never given a value is the nil interface and compares equal. A
// real pointer is non-nil the ordinary way, and the empty interface holds a typed
// nil the same as a named interface does.
func TestTypedNil(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"a returned typed nil is not the nil interface",
			typednil + "func makeErr(fail bool) error {\n\tvar p *MyError\n\tif fail {\n\t\tp = &MyError{7}\n\t}\n\treturn p\n}\n\nfunc main() {\n\tfmt.Println(makeErr(false) == nil, makeErr(true) == nil)\n}\n",
		},
		{
			"a bare error is the nil interface",
			typednil + "func main() {\n\tvar err error\n\tfmt.Println(err == nil)\n}\n",
		},
		{
			"an assigned typed nil is not the nil interface",
			typednil + "func main() {\n\tvar p *MyError\n\tvar err error = p\n\tfmt.Println(err == nil, err != nil)\n}\n",
		},
		{
			"an explicit conversion of a typed nil is not the nil interface",
			typednil + "func main() {\n\tvar p *MyError\n\tfmt.Println(error(p) == nil)\n}\n",
		},
		{
			"a real pointer converts to a non-nil interface",
			typednil + "func main() {\n\te := &MyError{7}\n\tfmt.Println(error(e) == nil)\n}\n",
		},
		{
			"the empty interface holds a typed nil the same way",
			typednil + "func main() {\n\tvar p *MyError\n\tvar i interface{} = p\n\tfmt.Println(i == nil, interface{}(p) == nil)\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestTypedNilEmit pins that a conversion to an interface type is the identity under
// elision, the concrete value already being the interface value, so error(p) lowers
// to p and the nil comparison that follows is an identity test against None.
func TestTypedNilEmit(t *testing.T) {
	t.Parallel()
	src := typednil + "func box(p *MyError) error {\n\treturn error(p)\n}\n\nfunc main() {\n\tvar p *MyError\n\tfmt.Println(box(p) == nil)\n}\n"
	got := emitOf(t, src)
	wants := []string{
		"def box(p):\n    return p\n",
		"(box(p) is None)",
	}
	for _, w := range wants {
		if !bytesContains(got, w) {
			t.Errorf("emitted main.py = \n%s\nwant it to contain\n%s", got, w)
		}
	}
}
