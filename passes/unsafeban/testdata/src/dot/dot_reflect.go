package dot

import (
	. "reflect"
)

func dotReflect() {
	f := NewAt // want `SAFE001: reflect.NewAt writes or exposes memory through reflection`
	_ = f
	var h SliceHeader // want `SAFE001: reflect.SliceHeader writes or exposes memory through reflection`
	_ = h
}
