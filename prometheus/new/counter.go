package prometheus

import (
	"fmt"
	"io"
	"sort"
	"sync"

	"code.google.com/p/goprotobuf/proto"
	"github.com/matttproud/golang_protobuf_extensions/ext"

	"encoding/json"
	model "github.com/prometheus/client_model/go"
)

// CounterOptions defines the behavior of CounterFamily.
type CounterOptions struct {
	MetricOptions

	// DefaultValue is the target value for all children once they have been
	// reset.
	DefaultValue float64
}

type CounterPartial interface {
	Clone() CounterPartial

	With(labels ...string)

	Apply() Counter
}

type counterPartial struct {
	sync.RWMutex

	labels labelPairs
	parent *counterFamily
}

func (p *counterPartial) validate() {
	if len(p.labels) != len(p.parent.options.Dimensions) {
		panic(fmt.Sprintf("illegal labels: wrong dimensions"))
	}

	unaccountedForDimensions := map[string]bool{}
	for _, dimension := range p.parent.options.Dimensions {
		unaccountedForDimensions[dimension] = true
	}

	for _, pair := range p.labels {
		if _, has := unaccountedForDimensions[pair.Name]; !has {
			panic(fmt.Sprintf("illegal labels: %s does not match defined dimensions", pair))
		}

		delete(unaccountedForDimensions, pair.Name)
	}
}

func (p *counterPartial) Apply() Counter {
	p.Lock()
	defer p.Unlock()

	if !sort.IsSorted(p.labels) {
		sort.Sort(p.labels)
	}

	fingerprint := p.labels.fingerprint()
	if counter, has := p.parent.find(fingerprint); has {
		return counter
	}

	p.validate()

	counter := &counter{
		fingerprint: fingerprint,
		parent:      p.parent,
		Labels:      p.labels,
		Value:       p.parent.options.DefaultValue,
	}

	p.parent.register(counter)

	return counter
}

func (p *counterPartial) Clone() CounterPartial {
	p.RLock()
	defer p.RUnlock()

	labels := make(labelPairs, len(p.labels))
	copy(labels, p.labels)

	return &counterPartial{
		labels: labels,
		parent: p.parent,
	}
}

func (p *counterPartial) With(labels ...string) {
	p.Lock()
	defer p.Unlock()

	if len(labels)%2 != 0 {
		panic(fmt.Sprintf("illegal labels: %s", labels))
	}

	for i := 0; i < len(labels); i += 2 {
		p.labels = append(p.labels, labelPair{
			Name:  labels[i],
			Value: labels[i+1],
		})
	}
}

type Counter interface {
	Increment()
	IncrementBy(float64)
	Decrement()
	DecrementBy(float64)
	Set(float64)

	Forget()
	Reset()
}

type counter struct {
	sync.RWMutex

	Labels labelPairs

	Value float64

	fingerprint uint64
	parent      *counterFamily
}

func (c *counter) Decrement() {
	c.Lock()
	defer c.Unlock()

	c.Value--
}

func (c *counter) DecrementBy(v float64) {
	c.Lock()
	defer c.Unlock()

	c.Value -= v
}

func (c *counter) Increment() {
	c.Lock()
	defer c.Unlock()

	c.Value++
}

func (c *counter) IncrementBy(v float64) {
	c.Lock()
	defer c.Unlock()

	c.Value += v
}

func (c *counter) Set(v float64) {
	c.Lock()
	defer c.Unlock()

	c.Value = v
}

func (c *counter) Forget() {
	c.parent.forget(c.fingerprint)
}

func (c *counter) Reset() {
	c.Lock()
	defer c.Unlock()

	c.Value = c.parent.options.DefaultValue
}

func (c *counter) asProto() *model.Metric {
	c.RLock()
	defer c.RUnlock()

	metric := &model.Metric{
		Counter: &model.Counter{
			Value: proto.Float64(c.Value),
		},
	}

	for _, pair := range c.Labels {
		labelPair := &model.LabelPair{
			Name:  proto.String(pair.Name),
			Value: proto.String(pair.Value),
		}

		metric.Label = append(metric.Label, labelPair)
	}

	return metric
}

func (c *counter) asText() string {
	c.RLock()
	defer c.RUnlock()

	return fmt.Sprintf("{%s}: %f", c.Labels, c.Value)
}

func (c *counter) Before(o *counter) bool {
	return c.Labels.Before(o.Labels)
}

func NewCounterFamily(o CounterOptions) CounterFamily {
	o.validate()

	family := &counterFamily{
		name:        o.deriveName(),
		options:     &o,
		childrenSet: map[uint64]int{},
	}

	defaultRegistry.register(family)

	return family
}

type CounterFamily interface {
	Family

	NewChild(labels ...string) CounterPartial
}

type counterChildren []*counter

func (c counterChildren) Len() int {
	return len(c)
}

func (c counterChildren) Less(i, j int) bool {
	return c[i].Before(c[j])
}

func (c counterChildren) Swap(i, j int) {
	c[i], c[j] = c[j], c[i]
}

type counterFamily struct {
	sync.RWMutex

	children    counterChildren
	childrenSet map[uint64]int

	options *CounterOptions

	name familyName
	fp   uint64
}

func (f *counterFamily) familyName() familyName {
	return f.name
}

func (f *counterFamily) fingerprint() uint64 {
	return f.fp
}

func (f *counterFamily) ForgetAll() {
	f.Lock()
	defer f.Unlock()

	f.children = counterChildren{}
	f.childrenSet = map[uint64]int{}
}

func (f *counterFamily) ResetAll() {
	f.RLock()
	defer f.RUnlock()

	for _, child := range f.children {
		child.Reset()
	}
}

func (f *counterFamily) forget(fingerprint uint64) {
	f.Lock()
	defer f.Unlock()

	index, ok := f.childrenSet[fingerprint]
	if !ok {
		panic("illegal invariant: missing fingerprint")
	}

	delete(f.childrenSet, fingerprint)
	switch index {
	case 0:
		f.children = f.children[1:]
	case len(f.children) - 1:
		f.children = f.children[:index-1]
	default:
		children := make(counterChildren, 0, len(f.children)-1)
		children = append(children, f.children[:index-1]...)
		children = append(children, f.children[index+1:]...)
		f.children = children
	}
}

func (f *counterFamily) find(fingerprint uint64) (*counter, bool) {
	f.RLock()
	defer f.RUnlock()

	index, present := f.childrenSet[fingerprint]
	if !present {
		return nil, false
	}

	return f.children[index], true
}

func (f *counterFamily) register(c *counter) {
	f.Lock()
	defer f.Unlock()

	f.children = append(f.children, c)
	// BUG(matt): Insertion sort: Evaluate whether this is OK after initial
	// server warmup.
	sort.Sort(f.children)
	for i, c := range f.children {
		f.childrenSet[c.fingerprint] = i
	}
}

func (f *counterFamily) NewChild(labels ...string) CounterPartial {
	if len(labels)%2 != 0 {
		panic(fmt.Sprintf("illegal labels: %s", labels))
	}

	pairs := labelPairs{}
	for i := 0; i < len(labels); i += 2 {
		pairs = append(pairs, labelPair{
			Name:  labels[i],
			Value: labels[i+1],
		})
	}

	return &counterPartial{
		labels: pairs,
		parent: f,
	}
}

func (f *counterFamily) dumpProto(w io.Writer, o *dumpOptions) error {
	f.RLock()
	defer f.RUnlock()

	m := &model.MetricFamily{
		Name: proto.String(f.name.String()),
		Type: model.MetricType_COUNTER.Enum(),
	}

	if o.includeHelp {
		m.Help = proto.String(f.options.Help)
	}

	for _, child := range f.children {
		m.Metric = append(m.Metric, child.asProto())
	}
	_, err := ext.WriteDelimited(w, m)

	return err
}

func (f *counterFamily) dumpText(w io.Writer, o *dumpOptions) error {
	f.RLock()
	defer f.RUnlock()

	for _, child := range f.children {
		_, err := fmt.Fprintf(w, "%s%s\n", f.name, child.asText())
		if err != nil {
			return err
		}
	}

	return nil
}

func (f *counterFamily) MarshalJSON() ([]byte, error) {
	f.RLock()
	defer f.RLock()

	// BUG(matt): Include docstring when requested.

	obj := map[string]interface{}{
		"Name":     f.name,
		"Children": f.children,
		"Type":     "counter",
	}

	return json.Marshal(obj)
}

func (f *counterFamily) shouldDump(*dumpOptions) bool {
	f.RLock()
	defer f.RUnlock()

	return len(f.children) > 0
}
