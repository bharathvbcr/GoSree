package b

import "errxpkg/a"

func BoxMayFail(bad bool) error {
	return a.MayFail(bad) // want "ERR004"
}

func BoxGuarded(bad bool) error {
	if e := a.MayFail(bad); e != nil {
		return e
	}
	return nil
}

func BoxAlwaysErr() error {
	return a.AlwaysErr()
}
