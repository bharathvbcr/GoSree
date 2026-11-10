package a

type DomainErr struct {
	Code int
}

func (e *DomainErr) Error() string { return "domain error" }

func MayFail(bad bool) *DomainErr { // want MayFail:"may_return_nil_concrete_err"
	if bad {
		return &DomainErr{Code: 500}
	}
	return nil
}

func AlwaysErr() *DomainErr { // want AlwaysErr:"non_nil_concrete_err"
	return &DomainErr{Code: 400}
}
