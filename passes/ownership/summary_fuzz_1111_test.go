package ownership

import (
	"reflect"
	"testing"
)

// factPrefix is a FuncFact payload up to and including the empty links count: flags, five empty
// bool slices, and zero links.
var factPrefix = []byte{0, 0, 0, 0, 0, 0, 0}

func decodeFact(data []byte) (*FuncFact, error) {
	f := &FuncFact{}
	return f, f.GobDecode(data)
}

// A corrupt fact must be rejected, never panic the analysis that imports it.
func TestFuncFactDecodeRejectsCorruptLengths(t *testing.T) {
	huge := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01} // uvarint 2^64-1
	cases := map[string][]byte{
		// One acquire whose step string claims 2^64-1 bytes: int(sLen) is -1.
		"steps length wraps negative": append(append(append([]byte{}, factPrefix...), 1, 0, 0), huge...),
		// A step length one past what is left.
		"steps length past the end": append(append([]byte{}, factPrefix...), 1, 0, 0, 5, 'a', 'b', 'c', 'd', 0),
		// A million links claimed by a payload with none.
		"links count past the data": append([]byte{0, 0, 0, 0, 0, 0}, 0x80, 0x80, 0x40),
		// A million acquires claimed by a payload with none.
		"acquires count past the data": append(append([]byte{}, factPrefix...), 0x80, 0x80, 0x40),
		"empty":                        {},
		"flags only":                   {0},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeFact(data); err == nil {
				t.Fatalf("GobDecode accepted corrupt payload %v", data)
			}
		})
	}
}

// FuzzFuncFactDecode: decoding arbitrary bytes never panics, and whatever decodes re-encodes to a
// payload that decodes to the same fact.
func FuzzFuncFactDecode(f *testing.F) {
	seeds := []*FuncFact{
		{},
		{Fresh: true, Unknown: true, Leaks: []bool{true, false, true}, Writes: []bool{true}},
		{
			Leaks: []bool{true, false, false, true, false, false, false, false, true}, Iso: []bool{false, true},
			Spawns: []bool{false, false, true}, FreshResult: []bool{true, false}, Writes: []bool{true, false, true},
			Links:    []Link{{A: 0, DepthA: 1, B: 2, DepthB: 0}, {A: -1, DepthA: 3, B: 4, DepthB: 5}},
			Acquires: []acquire{{Param: 0, Free: -1, Steps: ".mu", Mode: lockWrite}, {Param: -1, Free: 2, Mode: lockRead}},
		},
	}
	for _, s := range seeds {
		raw, err := s.GobEncode()
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add(append(append(append([]byte{}, factPrefix...), 1, 0, 0), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01))
	f.Fuzz(func(t *testing.T, data []byte) {
		first, err := decodeFact(data)
		if err != nil {
			return
		}
		raw, err := first.GobEncode()
		if err != nil {
			t.Fatalf("GobEncode of a decoded fact: %v", err)
		}
		second, err := decodeFact(raw)
		if err != nil {
			t.Fatalf("re-decoding an encoded fact: %v", err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("round trip changed the fact:\n first %+v\nsecond %+v", first, second)
		}
	})
}
