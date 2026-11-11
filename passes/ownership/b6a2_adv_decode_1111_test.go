package ownership

import (
	"encoding/binary"
	"testing"
)

func advUvarints(vs ...uint64) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.AppendUvarint(b, v)
	}
	return b
}

func advVarints(vs ...int64) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.AppendVarint(b, v)
	}
	return b
}

// Hand-crafted corrupt payloads, each of which must be rejected or decoded without a panic and
// without an allocation sized from the claimed count.
func TestAdvFuncFactDecodeCorruptNeverPanics(t *testing.T) {
	// flags + five empty bool slices.
	head := []byte{0, 0, 0, 0, 0, 0}
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	maxU := uint64(1<<64 - 1)
	cases := map[string][]byte{
		"bool slice length 2^64-1":              cat([]byte{0}, advUvarints(maxU)),
		"bool slice length 2^20 with no data":   cat([]byte{0}, advUvarints(1<<20)),
		"bool slice length 2^20+1":              cat([]byte{0}, advUvarints(1<<20+1)),
		"bool slice length one byte short":      cat([]byte{0}, advUvarints(17), []byte{0xff, 0xff}),
		"links 2^63":                            cat(head, advUvarints(1<<63)),
		"links exactly data/4 + 1":              cat(head, advUvarints(2), make([]byte, 7)),
		"links 1 with three varints":            cat(head, advUvarints(1), advVarints(1, 2, 3)),
		"link values min int64":                 cat(head, advUvarints(1), advVarints(-1<<63, -1<<63, 1<<63-1, 1<<63-1), advUvarints(0)),
		"link varint overflow (11 bytes)":       cat(head, advUvarints(1), []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}, advVarints(0, 0, 0)),
		"acquires 2^63":                         cat(head, advUvarints(0), advUvarints(1<<63)),
		"acquire step length max":               cat(head, advUvarints(0, 1), advVarints(-1<<63, -1<<63), advUvarints(maxU), []byte{1}),
		"acquire step length 2^63":              cat(head, advUvarints(0, 1), advVarints(0, 0), advUvarints(1<<63), []byte{1}),
		"acquire step length exactly rest":      cat(head, advUvarints(0, 1), advVarints(0, 0), advUvarints(2), []byte{'a', 'b'}),
		"acquire step length rest-1, mode ok":   cat(head, advUvarints(0, 1), advVarints(0, 0), advUvarints(1), []byte{'a', 3}),
		"acquire with no mode byte":             cat(head, advUvarints(0, 1), advVarints(0, 0), advUvarints(0)),
		"acquire count 2 but one acquire":       cat(head, advUvarints(0, 2), advVarints(0, 0), advUvarints(0), []byte{1}),
		"acquire mode byte 255":                 cat(head, advUvarints(0, 1), advVarints(0, 0), advUvarints(0), []byte{255}),
		"trailing garbage after a valid fact":   cat(head, advUvarints(0, 0), []byte{9, 9, 9}),
		"nil":                                   nil,
		"flags with high bits":                  {0xff},
		"truncated inside second bool slice":    {0, 0, 5},
		"single-byte varint continuation flags": {0, 0x80},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("GobDecode panicked on %q: %v", name, r)
				}
			}()
			f := &FuncFact{}
			err := f.GobDecode(data)
			t.Logf("err=%v links=%d acquires=%d", err, len(f.Links), len(f.Acquires))
			if err == nil {
				// Whatever decodes must encode and decode again.
				raw, e := f.GobEncode()
				if e != nil {
					t.Fatal(e)
				}
				if e := (&FuncFact{}).GobDecode(raw); e != nil {
					t.Fatalf("re-decode of an encoded decoded fact: %v", e)
				}
			}
		})
	}
}

// ImmFact decoding accepts exactly one byte.
func TestAdvImmFactDecodeLengths(t *testing.T) {
	for _, data := range [][]byte{nil, {}, {1, 1}, make([]byte, 1<<16)} {
		if err := (&ImmFact{}).GobDecode(data); err == nil {
			t.Errorf("accepted a %d-byte ImmFact payload", len(data))
		}
	}
}
