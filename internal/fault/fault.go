package fault

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string                   { return e.Code }
func New(status int, code, message string) error { return &Error{status, code, message} }
