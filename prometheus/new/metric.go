package prometheus

import (
	"fmt"
)

// MetricOptions defines the metric family's characteristics.
type MetricOptions struct {
	// Name is the base name used for metric formulation.  It is required.
	Name string
	// Subsystem is used to scope metrics families of the same Name within a
	// given process space.
	//
	// For instance, API and asset fetching ...
	Subsystem string
	Namespace string

	Dimensions []string

	Help string
}

func (o *MetricOptions) deriveName() familyName {
	switch {
	case o.Namespace != "" && o.Subsystem != "":
		return familyName(fmt.Sprintf("%s_%s_%s", o.Namespace, o.Subsystem, o.Name))
	case o.Namespace != "":
		return familyName(fmt.Sprintf("%s_%s", o.Namespace, o.Name))
	case o.Subsystem != "":
		return familyName(fmt.Sprintf("%s_%s", o.Subsystem, o.Name))
	default:
		return familyName(o.Name)
	}
}

func (o *MetricOptions) validate() {
	switch {
	case o.Name == "":
		panic("illegal Name; one must be provided")
	case o.Help == "":
		panic("illegal Help; documentation must be provided")
	}
}
