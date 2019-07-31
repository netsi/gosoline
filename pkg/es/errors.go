package es

type DetailFields map[string]interface{}

type Error struct {
	Message string
	Status  int
	Fields  DetailFields
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) WithFields(fields DetailFields) {
	e.Fields = fields
}

func (e *Error) WithField(name string, value interface{}) {
	e.Fields[name] = value
}

func NewError(message string) *Error {
	return &Error{
		Message: message,
		Fields:  make(DetailFields),
	}
}
