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

// GaugeOptions defines the behavior of GaugeFamily.
type GaugeOptions struct {
	MetricOptions

	// DefaultValue is the target value for all children once they have been
	// reset.
	DefaultValue float64
}

type GaugePartial interface {
	Clone() GaugePartial

	With(labels ...string)

	Apply() Gauge
}

type gaugePartial struct {
	sync.RWMutex

	labels labelPairs
	parent *gaugeFamily
}

func (p *gaugePartial) validate() {
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

func (p *gaugePartial) Apply() Gauge {
	p.Lock()
	defer p.Unlock()

	if !sort.IsSorted(p.labels) {
		sort.Sort(p.labels)
	}

	fingerprint := p.labels.fingerprint()
	if gauge, has := p.parent.find(fingerprint); has {
		return gauge
	}

	p.validate()

	gauge := &gauge{
		fingerprint: fingerprint,
		parent:      p.parent,
		Labels:      p.labels,
		Value:       p.parent.options.DefaultValue,
	}

	p.parent.register(gauge)

	return gauge
}

func (p *gaugePartial) Clone() GaugePartial {
	p.RLock()
	defer p.RUnlock()

	labels := make(labelPairs, len(p.labels))
	copy(labels, p.labels)

	return &gaugePartial{
		labels: labels,
		parent: p.parent,
	}
}

func (p *gaugePartial) With(labels ...string) {
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

type Gauge interface {
	Set(float64)

	Forget()
	Reset()
}

type gauge struct {
	sync.RWMutex

	Labels labelPairs

	Value float64

	fingerprint uint64
	parent      *gaugeFamily
}

func (c *gauge) Set(v float64) {
	c.Lock()
	defer c.Unlock()

	c.Value = v
}

func (c *gauge) Forget() {
	c.parent.forget(c.fingerprint)
}

func (c *gauge) Reset() {
	c.Lock()
	defer c.Unlock()

	c.Value = c.parent.options.DefaultValue
}

func (c *gauge) asProto() *model.Metric {
	c.RLock()
	defer c.RUnlock()

	metric := &model.Metric{
		Gauge: &model.Gauge{
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

func (c *gauge) asText() string {
	c.RLock()
	defer c.RUnlock()

	return fmt.Sprintf("{%s}: %f", c.Labels, c.Value)
}

func (c *gauge) Before(o *gauge) bool {
	return c.Labels.Before(o.Labels)
}

func NewGaugeFamily(o GaugeOptions) GaugeFamily {
	o.validate()

	family := &gaugeFamily{
		name:        o.deriveName(),
		options:     &o,
		childrenSet: map[uint64]int{},
	}

	defaultRegistry.register(family)

	return family
}

type GaugeFamily interface {
	Family

	NewChild(labels ...string) GaugePartial
}

type gaugeChildren []*gauge

func (c gaugeChildren) Len() int {
	return len(c)
}

func (c gaugeChildren) Less(i, j int) bool {
	return c[i].Before(c[j])
}

func (c gaugeChildren) Swap(i, j int) {
	c[i], c[j] = c[j], c[i]
}

type gaugeFamily struct {
	sync.RWMutex

	children    gaugeChildren
	childrenSet map[uint64]int

	options *GaugeOptions

	name familyName
	fp   uint64
}

func (f *gaugeFamily) familyName() familyName {
	return f.name
}

func (f *gaugeFamily) fingerprint() uint64 {
	return f.fp
}

func (f *gaugeFamily) ForgetAll() {
	f.Lock()
	defer f.Unlock()

	f.children = []*gauge{}
	f.childrenSet = map[uint64]int{}
}

func (f *gaugeFamily) ResetAll() {
	f.RLock()
	defer f.RUnlock()

	for _, child := range f.children {
		child.Reset()
	}
}

func (f *gaugeFamily) forget(fingerprint uint64) {
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
		children := make(gaugeChildren, 0, len(f.children)-1)
		children = append(children, f.children[:index-1]...)
		children = append(children, f.children[index+1:]...)
		f.children = children
	}
}

func (f *gaugeFamily) find(fingerprint uint64) (*gauge, bool) {
	f.RLock()
	defer f.RUnlock()

	index, present := f.childrenSet[fingerprint]
	if !present {
		return nil, false
	}

	return f.children[index], true
}

func (f *gaugeFamily) register(c *gauge) {
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

func (f *gaugeFamily) NewChild(labels ...string) GaugePartial {
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

	return &gaugePartial{
		labels: pairs,
		parent: f,
	}
}

func (f *gaugeFamily) dumpProto(w io.Writer, o *dumpOptions) error {
	f.RLock()
	defer f.RUnlock()

	m := &model.MetricFamily{
		Name: proto.String(f.name.String()),
		Type: model.MetricType_GAUGE.Enum(),
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

func (f *gaugeFamily) dumpText(w io.Writer, o *dumpOptions) error {
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

func (f *gaugeFamily) MarshalJSON() ([]byte, error) {
	f.RLock()
	defer f.RLock()

	// BUG(matt): Include docstring when requested.

	obj := map[string]interface{}{
		"Name":     f.name,
		"Children": f.children,
		"Type":     "gauge",
	}

	return json.Marshal(obj)
}

func (f *gaugeFamily) shouldDump(*dumpOptions) bool {
	f.RLock()
	defer f.RUnlock()

	return len(f.children) > 0
}
