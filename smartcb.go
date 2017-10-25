// packate smartcb implements a `TripFunc` for github.com/rubyist/circuitbreaker
// that automatically adjusts the tripping error threshold based on abnormal
// increase in error rate.
//
// The circuit breaker starts off with a learning phase for understanding the error
// profile of the wrapped command and then adjusts its tripping threshold for error
// rate based on what it has learned.
//
// The error threshold is calculated as an exponential weighted moving average which
// smoothens out jitter and can detect rapid changes in error rate, allowing for the
// circuit to trip fast in case of a rapid degradation of the wrapped command.

package smartcb

import (
	"sync"
	"time"

	"github.com/rubyist/circuitbreaker"
)

const minFail = 0.001
const maxFail = 0.4
const learningCycles = 10    // Size of learning window relative to decision window
const relearningCycles = 100 // No. of decision cycles after which to re-trigger learning

func New(decisionWindow time.Duration) *circuit.Breaker {
	if decisionWindow <= 0 {
		panic("smartcb.NewSmartTripper: decisionWindow should be a valid duration")
	}

	options := &circuit.Options{
		WindowTime: decisionWindow,
	}

	var initTime time.Time
	var mu sync.Mutex
	rate := 0.0

	recordError := func(cb *circuit.Breaker) float64 {
		mu.Lock()
		defer mu.Unlock()
		cbr := cb.ErrorRate()
		rate = (learningCycles*rate + cbr) / (learningCycles + 1)
		return cbr
	}

	tripper := func(cb *circuit.Breaker) bool {
		tElapsed := time.Since(initTime)

		// Initiate Learning Phase
		if initTime == (time.Time{}) || tElapsed > decisionWindow*relearningCycles {
			initTime = time.Now()
			tElapsed = time.Since(initTime)
		}

		// Learning
		cycles := float64(tElapsed) / float64(decisionWindow)
		if cycles < learningCycles {
			mu.Lock()
			// Trip rate starts with maxFail and approaches the learned rate as learning nears completion
			tripRate := rate*cycles/float64(learningCycles) + maxFail*(float64(learningCycles)-cycles)/float64(learningCycles)
			mu.Unlock()
			return tripRate < recordError(cb)
		}

		mu.Lock()
		defer mu.Unlock()
		return rate < cb.ErrorRate() && cb.Failures()+cb.Successes() > 100
	}

	options.ShouldTrip = tripper
	return circuit.NewBreakerWithOptions(options)
}
