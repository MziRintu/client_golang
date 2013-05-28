package prometheus

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
)

type Family interface {
	ResetAll()
	ForgetAll()

	familyName() familyName
	fingerprint() uint64

	shouldDump(*dumpOptions) bool
	dumpProto(w io.Writer, o *dumpOptions) error
	dumpText(w io.Writer, o *dumpOptions) error
}

type families []Family

func (f families) Len() int {
	return len(f)
}

func (f families) Swap(i, j int) {
	f[i], f[j] = f[j], f[i]
}

func (f families) Less(i, j int) bool {
	return f[i].familyName() < f[j].familyName()
}

func (f families) dump(w io.Writer, o *dumpOptions) error {
	switch o.format {
	case dumpProto:
		return f.dumpProto(w, o)
	case dumpText:
		return f.dumpText(w, o)
	case dumpJSON:
		return f.dumpJSON(w, o)
	default:
		panic(fmt.Sprintf("illegal format: %s", o.format))
	}
}

func (f families) dumpProto(w io.Writer, o *dumpOptions) error {
	for _, family := range f {
		if err := family.dumpProto(w, o); err != nil {
			return err
		}
	}

	return nil
}

func (f families) dumpText(w io.Writer, o *dumpOptions) error {
	for _, family := range f {
		if err := family.dumpText(w, o); err != nil {
			return err
		}
	}

	return nil
}

func (f families) dumpJSON(w io.Writer, o *dumpOptions) error {
	return json.NewEncoder(w).Encode(f)
}

type familyName string

func (n familyName) fingerprint() uint64 {
	hash := fnv.New64a()

	fmt.Fprint(hash, string(n))

	return hash.Sum64()
}

func (n familyName) String() string {
	return string(n)
}
