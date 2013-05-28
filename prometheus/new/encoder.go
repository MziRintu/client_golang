package prometheus

import (
	"io"
)

type protoEncoder interface {
	dumpProto(io.Writer) error
}
