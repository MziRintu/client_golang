package prometheus

import (
	"io"
	"testing"
)

type dummyFamily struct {
	name familyName
	fp   uint64
}

func (f dummyFamily) familyName() familyName {
	return f.name
}

func (dummyFamily) ResetAll() {}

func (dummyFamily) ForgetAll() {}

func (f dummyFamily) fingerprint() uint64 {
	return f.fp
}

func (dummyFamily) dumpProto(io.Writer, *dumpOptions) error {
	return nil
}

func (dummyFamily) dumpText(io.Writer, *dumpOptions) error {
	return nil
}

func (dummyFamily) shouldDump(*dumpOptions) bool {
	return true
}

func (dummyFamily) MarshalJSON() ([]byte, error) {
	return nil, nil
}

func testRegistration(t *testing.T, i, j int, r *registry, s bool, f Family) {
	defer func() {
		if !s {
			if err := recover(); err == nil {
				t.Fatalf("%d.%d. expected %s, got opposite", i, j, s)
			}
		}

		if s {
			if err := recover(); err != nil {
				t.Fatalf("%d.%d. expected %s, got opposite: %s", i, j, s, err)
			}
		}
	}()

	r.register(f)
}

func TestRegister(t *testing.T) {
	type in struct {
		families []dummyFamily
	}
	type out struct {
		success []bool
	}

	var scenarios = []struct {
		in  in
		out out
	}{
		{
			in: in{
				families: []dummyFamily{
					{
						name: "The Great",
					},
				},
			},
			out: out{
				success: []bool{true},
			},
		},
		{
			in: in{
				families: []dummyFamily{
					{
						name: "The Only Family",
					},
					{
						name: "The Only Family",
					},
				},
			},
			out: out{
				success: []bool{
					true,
					false,
				},
			},
		},
	}

	for i, scenario := range scenarios {
		registry := newRegistry()

		for j, family := range scenario.in.families {
			testRegistration(t, i, j, registry, scenario.out.success[j], family)
		}
	}
}
