package camera

type printfLogger interface {
	Printf(string, ...any)
}

func logPrintf(logger any, format string, args ...any) {
	switch value := logger.(type) {
	case nil:
		return
	case func(string, ...any):
		value(format, args...)
	case printfLogger:
		value.Printf(format, args...)
	}
}
