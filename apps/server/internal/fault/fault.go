package fault

// Error carries a safe public error and its HTTP classification.
type Error struct {
	Status  int
	Code    string
	Message string
}

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func (err *Error) Error() string { return err.Message }
