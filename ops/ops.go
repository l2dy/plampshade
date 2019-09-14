// Package ops provides a facility for tracking the processing of operations,
// including contextual metadata about the operation and their final success or
// failure. An op is assumed to have succeeded if by the time of calling Exit()
// no errors have been reported. The final status can be reported to a metrics
// facility.
package ops

import (
	"sync"
	"sync/atomic"

	"github.com/l2dy/plampshade/context"
)

var (
	cm             = context.NewManager()
	reporters      []Reporter
	reportersMutex sync.RWMutex
)

// Reporter is a function that reports the success or failure of an Op. If
// failure is nil, the Op can be considered successful.
type Reporter func(failure error, ctx map[string]interface{})

// Op represents an operation that's being performed. It mimics the API of
// context.Context.
type Op interface {
	// Begin marks the beginning of an Op under this Op.
	Begin(name string) Op

	// Go starts the given function on a new goroutine.
	Go(fn func())

	// End marks the end of this op, at which point the Op will report its success
	// or failure to all registered Reporters.
	End()

	// Cancel cancels this op so that even if End() is called later, it will not
	// report its success or failure.
	Cancel()

	// Set puts a key->value pair into the current Op's context.
	Set(key string, value interface{}) Op

	// SetDynamic puts a key->value pair into the current Op's context, where the
	// value is generated by a function that gets evaluated at every Read.
	SetDynamic(key string, valueFN func() interface{}) Op

	// FailIf marks this Op as failed if the given err is not nil. If FailIf is
	// called multiple times, the latest error will be reported as the failure.
	// Returns the original error for convenient chaining.
	FailIf(err error) error
}

type op struct {
	ctx      context.Context
	canceled bool
	failure  atomic.Value
}

// RegisterReporter registers the given reporter.
func RegisterReporter(reporter Reporter) {
	reportersMutex.Lock()
	reporters = append(reporters, reporter)
	reportersMutex.Unlock()
}

// Begin marks the beginning of a new Op.
func Begin(name string) Op {
	return &op{ctx: cm.Enter().Put("op", name).PutIfAbsent("root_op", name)}
}

func (o *op) Begin(name string) Op {
	return &op{ctx: o.ctx.Enter().Put("op", name).PutIfAbsent("root_op", name)}
}

func (o *op) Go(fn func()) {
	o.ctx.Go(fn)
}

// Go mimics the method from context.Manager.
func Go(fn func()) {
	cm.Go(fn)
}

func (o *op) Cancel() {
	o.canceled = true
}

func (o *op) End() {
	if o.canceled {
		return
	}

	var reportersCopy []Reporter
	reportersMutex.RLock()
	if len(reporters) > 0 {
		reportersCopy = make([]Reporter, len(reporters))
		copy(reportersCopy, reporters)
	}
	reportersMutex.RUnlock()

	if len(reportersCopy) > 0 {
		var failure error
		_failure := o.failure.Load()
		ctx := o.ctx.AsMap(_failure, true)
		if _failure != nil {
			failure = _failure.(error)
			_, errorSet := ctx["error"]
			if !errorSet {
				ctx["error"] = failure.Error()
			}
		}
		for _, reporter := range reportersCopy {
			reporter(failure, ctx)
		}
	}

	o.ctx.Exit()
}

func (o *op) Set(key string, value interface{}) Op {
	o.ctx.Put(key, value)
	return o
}

// SetGlobal puts a key->value pair into the global context, which is inherited
// by all Ops.
func SetGlobal(key string, value interface{}) {
	cm.PutGlobal(key, value)
}

func (o *op) SetDynamic(key string, valueFN func() interface{}) Op {
	o.ctx.PutDynamic(key, valueFN)
	return o
}

// SetGlobalDynamic is like SetGlobal but uses a function to derive the value
// at read time.
func SetGlobalDynamic(key string, valueFN func() interface{}) {
	cm.PutGlobalDynamic(key, valueFN)
}

// AsMap mimics the method from context.Manager.
func AsMap(obj interface{}, includeGlobals bool) context.Map {
	return cm.AsMap(obj, includeGlobals)
}

func (o *op) FailIf(err error) error {
	if err != nil {
		o.failure.Store(err)
	}
	return err
}
