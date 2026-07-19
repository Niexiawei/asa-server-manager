package iox

type LogWriter struct {
	LoggerFn func(string)
}

func (lw *LogWriter) Write(
	p []byte,
) (int, error) {

	if lw.LoggerFn != nil {

		lw.LoggerFn(
			string(p),
		)

	}

	return len(p), nil
}
