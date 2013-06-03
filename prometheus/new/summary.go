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

type QuantilePair struct {
	Quantile float64
	Accuracy float64
}

type QuantilePairs []QuantilePair

// SummaryOptions defines the behavior of SummaryFamily.
type SummaryOptions struct {
	MetricOptions

	RequestedQuantiles QuantilePairs
}

func (s *SummaryOptions) validate() {
	s.MetricOptions.validate()

	if len(s.RequestedQuantiles) == 0 {
		panic(fmt.Sprintf("illegal summarization: must request at least one quantile"))
	}
}

type SummaryPartial interface {
	Clone() SummaryPartial

	With(labels ...string)

	Apply() Summary
}

type summaryPartial struct {
	sync.RWMutex

	labels labelPairs
	parent *summaryFamily
}

func (p *summaryPartial) validate() {
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

func (p *summaryPartial) Apply() Summary {
	p.Lock()
	defer p.Unlock()

	if !sort.IsSorted(p.labels) {
		sort.Sort(p.labels)
	}

	fingerprint := p.labels.fingerprint()
	if summary, has := p.parent.find(fingerprint); has {
		return summary
	}

	p.validate()

	summary := &summary{
		fingerprint: fingerprint,
		parent:      p.parent,
		Labels:      p.labels,
	}

	p.parent.register(summary)

	return summary
}

func (p *summaryPartial) Clone() SummaryPartial {
	p.RLock()
	defer p.RUnlock()

	labels := make(labelPairs, len(p.labels))
	copy(labels, p.labels)

	return &summaryPartial{
		labels: labels,
		parent: p.parent,
	}
}

func (p *summaryPartial) With(labels ...string) {
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

type Summary interface {
	Sample(float64)

	Forget()
	Reset()
}

type summary struct {
	sync.RWMutex

	Labels labelPairs

	fingerprint uint64
	parent      *summaryFamily
}

func (c *summary) Sample(_ float64) {
	// BUG(matt): Not implemented.

	c.Lock()
	defer c.Unlock()
}

func (c *summary) Forget() {
	c.parent.forget(c.fingerprint)
}

func (*summary) Reset() {
	// BUG(matt): Not implemented.
}

func (c *summary) asProto() *model.Metric {
	// BUG(matt): Not implemented.

	c.RLock()
	defer c.RUnlock()

	metric := &model.Metric{
		Summary: &model.Summary{},
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

func (*summary) asText() string {
	// BUG(matt): Not implemented.
	return "none"
}

func (c *summary) Before(o *summary) bool {
	return c.Labels.Before(o.Labels)
}

func NewSummaryFamily(o SummaryOptions) SummaryFamily {
	o.validate()

	family := &summaryFamily{
		name:        o.deriveName(),
		options:     &o,
		childrenSet: map[uint64]int{},
	}

	defaultRegistry.register(family)

	return family
}

type SummaryFamily interface {
	Family

	NewChild(labels ...string) SummaryPartial
}

type summaryChildren []*summary

func (c summaryChildren) Len() int {
	return len(c)
}

func (c summaryChildren) Less(i, j int) bool {
	return c[i].Before(c[j])
}

func (c summaryChildren) Swap(i, j int) {
	c[i], c[j] = c[j], c[i]
}

type summaryFamily struct {
	sync.RWMutex

	children    summaryChildren
	childrenSet map[uint64]int

	options *SummaryOptions

	name familyName
	fp   uint64
}

func (f *summaryFamily) familyName() familyName {
	return f.name
}

func (f *summaryFamily) fingerprint() uint64 {
	return f.fp
}

func (f *summaryFamily) ForgetAll() {
	f.Lock()
	defer f.Unlock()

	f.children = summaryChildren{}
	f.childrenSet = map[uint64]int{}
}

func (f *summaryFamily) ResetAll() {
	f.RLock()
	defer f.RUnlock()

	for _, child := range f.children {
		child.Reset()
	}
}

func (f *summaryFamily) forget(fingerprint uint64) {
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
		children := make(summaryChildren, 0, len(f.children)-1)
		children = append(children, f.children[:index-1]...)
		children = append(children, f.children[index+1:]...)
		f.children = children
	}
}

func (f *summaryFamily) find(fingerprint uint64) (*summary, bool) {
	f.RLock()
	defer f.RUnlock()

	index, present := f.childrenSet[fingerprint]
	if !present {
		return nil, false
	}

	return f.children[index], true
}

func (f *summaryFamily) register(c *summary) {
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

func (f *summaryFamily) NewChild(labels ...string) SummaryPartial {
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

	return &summaryPartial{
		labels: pairs,
		parent: f,
	}
}

func (f *summaryFamily) dumpProto(w io.Writer, o *dumpOptions) error {
	f.RLock()
	defer f.RUnlock()

	m := &model.MetricFamily{
		Name: proto.String(f.name.String()),
		Type: model.MetricType_SUMMARY.Enum(),
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

func (f *summaryFamily) dumpText(w io.Writer, o *dumpOptions) error {
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

func (f *summaryFamily) MarshalJSON() ([]byte, error) {
	f.RLock()
	defer f.RLock()

	// BUG(matt): Include docstring when requested.

	obj := map[string]interface{}{
		"Name":     f.name,
		"Children": f.children,
		"Type":     "summary",
	}

	return json.Marshal(obj)
}

func (f *summaryFamily) shouldDump(*dumpOptions) bool {
	f.RLock()
	defer f.RUnlock()

	return len(f.children) > 0
}
