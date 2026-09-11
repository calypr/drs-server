package transfer

// NoOpLogger satisfies TransferLogger without emitting output.
type NoOpLogger struct{}

func (NoOpLogger) Error(string, ...any)  {}
func (NoOpLogger) Printf(string, ...any) {}
