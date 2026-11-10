package sub

type SubErr struct{ Msg string }

func (e *SubErr) Error() string { return e.Msg }

func MayFail(ok bool) *SubErr {
	if ok {
		return nil
	}
	return &SubErr{Msg: "failed"}
}

func AlwaysErr(msg string) *SubErr {
	return &SubErr{Msg: msg}
}
