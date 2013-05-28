package prometheus

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

type registry struct {
	sync.RWMutex

	families    families
	familiesSet map[uint64]int
}

func (r *registry) register(f Family) {
	r.Lock()
	defer r.Unlock()

	if _, has := r.familiesSet[f.fingerprint()]; has {
		panic(fmt.Sprintf("illegal metric: %s is already registered", f))
	}

	r.families = append(r.families, f)
	// BUG(matt): Insertion sort: Evaluate whether this is OK after initial
	// server warmup.
	sort.Sort(r.families)

	for i, f := range r.families {
		r.familiesSet[f.fingerprint()] = i
	}
}

func (r *registry) collectFamilies(o *dumpOptions) (f families) {
	r.RLock()
	defer r.RUnlock()

	for _, family := range r.families {
		if !family.shouldDump(o) {
			continue
		}

		f = append(f, family)
	}

	return f
}

func (r *registry) dump(w io.Writer, o *dumpOptions) error {
	// BUG(matt): This works with the assumption that no metric families would
	//            suddenly disappear due to having their children forgotten
	//            in-flight.
	return r.collectFamilies(o).dump(w, o)
}

func newRegistry() *registry {
	return &registry{
		familiesSet: map[uint64]int{},
	}
}

var defaultRegistry = newRegistry()
