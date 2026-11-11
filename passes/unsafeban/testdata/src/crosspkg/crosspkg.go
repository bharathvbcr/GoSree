package crosspkg

import "bridge"

func bypassViaAlias(p *int) *float64 {
	raw := bridge.RawAlias(p) // want `SAFE001: bridge.RawAlias exposes unsafe.Pointer`
	return (*float64)(raw)
}

func bypassViaDefinedType(p *int) bridge.RawDef { // want `SAFE001: bridge.RawDef exposes unsafe.Pointer`
	return nil
}

func bypassViaFuncReturn(p *int) *float64 {
	return (*float64)(bridge.Leak(p)) // want `SAFE001: bridge.Leak passes or returns unsafe.Pointer`
}

//gosree:unsafe explicit unsafe annotation still permits reviewed FFI callers
func allowedWhenAnnotated(p *int) *float64 {
	return (*float64)(bridge.Leak(p))
}
