package prometheus

type dumpFormat int

const (
	dumpProto dumpFormat = iota
	dumpText
	dumpJSON
)

// dumpOptions models the behavior for metrics serialization.
type dumpOptions struct {
	// includeHelp directs whether the metric's documentation should be bundled
	// in the over-the-wire transmission.
	includeHelp bool
	// format specifies the over-the-wire schema.
	format dumpFormat
}
