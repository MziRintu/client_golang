package prometheus

import (
	"fmt"
	"hash/fnv"
	"strings"
)

type labelPair struct {
	Name  string
	Value string
}

func (l *labelPair) Before(o *labelPair) bool {
	if l.Name < o.Name {
		return true
	}
	if l.Name > o.Name {
		return false
	}

	if l.Value < o.Value {
		return true
	}

	return false
}

func (l *labelPair) String() string {
	return fmt.Sprintf("%s=%s", l.Name, l.Value)
}

type labelPairs []labelPair

func (l labelPairs) Len() int {
	return len(l)
}

func (l labelPairs) Less(i, j int) bool {
	return l[i].Before(&l[j])
}

func (l labelPairs) Swap(i, j int) {
	l[i], l[j] = l[j], l[i]
}

func (l labelPairs) fingerprint() uint64 {
	digest := fnv.New64a()

	for _, pair := range l {
		fmt.Fprint(digest, pair.Name, pair.Value)
	}

	return digest.Sum64()
}

func (l labelPairs) Before(o labelPairs) bool {
	for i, pair := range l {
		if pair.Before(&o[i]) {
			return true
		}
	}

	return false
}

func (l labelPairs) Strings() []string {
	s := []string{}

	for _, p := range l {
		s = append(s, p.String())
	}

	return s
}

func (l labelPairs) String() string {
	return strings.Join(l.Strings(), ",")
}
