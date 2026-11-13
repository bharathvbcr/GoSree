package errcheck_test

import (
	"fmt"
	"strings"
	"testing"
)

// Adversarial pass over ft-6d5a. Each case runs the analyzer on a source file through the helper in
// msg_6d5a_test.go and pins the whole "line: message" list, so a moved, added or dropped finding fails
// as loudly as a changed message.

const advHdr6d5a = hdr6d5a + `
type S struct{}

func (S) M() error { return errNo }
`

func TestAdv6d5aMessages(t *testing.T) {
	const over = "ERR003: assignment to '%s' overwrites an earlier unchecked error"
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "unicode cell",
			src: advHdr6d5a + `
func U() error {
	var ñerr error
	ñerr = a()
	ñerr = b()
	return ñerr
}
`,
			want: []string{"25: " + fmt.Sprintf(over, "ñerr")},
		},
		{
			name: "cjk cell",
			src: advHdr6d5a + `
func U() error {
	var 错误 error
	错误 = a()
	错误 = b()
	return 错误
}
`,
			want: []string{"25: " + fmt.Sprintf(over, "错误")},
		},
		{
			name: "unicode shadow",
			src: advHdr6d5a + `
func U() error {
	ñ := a()
	if true {
		ñ := b()
		_ = ñ
	}
	return ñ
}
`,
			want: []string{"25: ERR003: inner 'ñ' declaration shadows outer 'ñ', losing the inner error on fallthrough"},
		},
		{
			name: "generic function cell",
			src: advHdr6d5a + `
func G[X any](x X) error {
	_ = x
	var err error
	err = a()
	err = b()
	return err
}
`,
			want: []string{"26: " + fmt.Sprintf(over, "err")},
		},
		{
			name: "method value call",
			src: advHdr6d5a + `
func MV() error {
	var err error
	m := S{}.M
	err = m()
	err = b()
	return err
}
`,
			want: []string{"26: " + fmt.Sprintf(over, "err")},
		},
		{
			name: "blank assignments report nothing",
			src: advHdr6d5a + `
func Blank() {
	_ = a()
	_ = b()
}
`,
			want: nil,
		},
		{
			name: "closure pair after closure pair",
			src: advHdr6d5a + `
func CC() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	func() { v, err = workV() }()
	if err != nil {
		return 0
	}
	return use(v)
}
`,
			// Pins current behaviour: two closures filling the same cells report ERR002 on the
			// uses and no ERR003, so the changed bind site is not what reports here.
			want: []string{
				"25: ERR002: result of m.workV() from fallible call is used before proving its companion error is nil",
				"26: ERR002: result of m.workV() from fallible call is used before proving its companion error is nil",
			},
		},
		{
			name: "twin after twin",
			src: advHdr6d5a + `
func TT() int {
	var v *T
	var err error
	v, err = workV()
	v, err = workV()
	if err != nil {
		return 0
	}
	return use(v)
}
`,
			want: []string{"26: " + fmt.Sprintf(over, "err")},
		},
		{
			// The no-closure twin of a closure pair that overwrites its own earlier error on a later
			// trip round a loop. The fix claims the twin names the variable.
			name: "twin overwriting itself in a loop",
			src: advHdr6d5a + `
func Loop() int {
	var v *T
	var err error
	i := 0
again:
	v, err = workV()
	i++
	if i < 2 {
		goto again
	}
	_ = err
	return use(v)
}
`,
			want: []string{
				"27: " + fmt.Sprintf(over, "err"),
				"33: ERR002: result of m.workV() from fallible call is used before proving its companion error is nil",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := messages6d5a(t, tc.src)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("messages:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}
