package smartcb

import (
	"math"
	"sync"
	"time"

	"github.com/rubyist/circuitbreaker"
)

// Policies for configuring the circuit breaker's decision making.
//
// MaxFail is the only parameter that might need adjustment.
// Do not tweak the other parameters unless you are a statistician.
// If you must, experiment with changing one parameter at a time.
// All parameters are required to be > 0
//
type Policies struct {
	// Absolute highest failure rate above which the breaker must open
	// Default is 0.4 (40%). You should definitely review this number
	MaxFail float64

	// Number of "decision windows" used for learning
	LearningWindowX float64
	// Number of "decision windows" after which learning is restarted.
	//
	// This setting must be greater than LearningWindowX otherwise the breaker
	// would be in a perpetual learning state
	ReLearningWindowX float64
	// Smoothing factor for error rate learning. Higher numbers reduce jitter
	// but cause more lag
	EWMADecayFactor float64
	// Number of trials in a decision window.
	SamplesPerWindow int64
}

var defaults = Policies{
	MaxFail:           0.4,
	LearningWindowX:   10.0,
	ReLearningWindowX: 100.0,
	EWMADecayFactor:   10.0,
	SamplesPerWindow:  1000,
}

func max(l, r float64) float64 {
	if l > r {
		return l
	}
	return r
}

// Circuit Breaker's Learning State
type State int

const (
	// Circuit Breaker has learned
	Learned State = iota

	// Circuit Breaker is learning
	Learning
)

const minFail = 0.001

func (s State) String() string {
	switch s {
	case Learning:
		return "Learning"
	case Learned:
		fallthrough
	default:
		return "Learned"
	}
}

// A Smart TripFunction Generator
//
// All circuit breakers obtained out of a generator
// share their learning state, but the circuit breaker state
// (error rates, event counts, etc.) is not shared
//
type SmartTripper struct {
	decisionWindow time.Duration
	policies       Policies
	state          State
	rate           float64
	initTime       time.Time
	mu             sync.Mutex
}

// Returns Policies initialised to default values
//
func NewPolicies() Policies {
	return defaults
}

// Create a SmartTripper based on the nominal QPS for your task
//
// "Nominal QPS" is the basis on which the SmartTripper configures its
// responsiveness settings. A suitable value for this parameter would be
// your median QPS. If your QPS varies a lot during operation, choosing this
// value closer to max QPS will make the circuit breaker more prone to tripping
// during low traffic periods and choosing a value closer to min QPS will make it
// slow to respond during high traffic periods.
//
// NOTE: Provide QPS value applicable for one instance of the circuit breaker,
// not the overall QPS across multiple instances.
//
func NewSmartTripper(QPS int, p Policies) *SmartTripper {
	if QPS <= 0 {
		panic("smartcb.NewSmartTripper: QPS should be >= 1")
	}
	decisionWindow := time.Millisecond * time.Duration(float64(p.SamplesPerWindow)*1000.0/float64(QPS))

	return &SmartTripper{decisionWindow: decisionWindow, policies: p, rate: minFail}
}

// Returns the Learning/Learned state of the Smart Tripper
//
func (t *SmartTripper) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.state
}

// Returns the error rate that has been learned by the Smart Tripper
//
func (t *SmartTripper) LearnedRate() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.rate
}

func (t *SmartTripper) tripFunc() circuit.TripFunc {
	learningCycles := t.policies.LearningWindowX
	relearningCycles := t.policies.ReLearningWindowX
	maxFail := t.policies.MaxFail

	initLearning := func(cb *circuit.Breaker) {
		t.initTime = time.Now()
		t.state = Learning
	}

	recordError := func(cb *circuit.Breaker) float64 {
		cbr := cb.ErrorRate()
		t.rate = max((t.policies.EWMADecayFactor*t.rate+cbr)/(t.policies.EWMADecayFactor+1), minFail)
		if t.rate > maxFail {
			t.rate = maxFail
		}

		return cbr
	}

	// Use Adjusted Wald Method to estimate whether we are confident enough to trip based on the no. of samples
	shouldPerhapsTrip := func(target, actual float64, sampleSize int64) bool {
		if sampleSize < t.policies.SamplesPerWindow/10 { // Can't guess much from just 10% samples
			return false
		}
		ss := float64(sampleSize)
		ssig := float64(t.policies.SamplesPerWindow)
		if ss > ssig {
			ss = ssig
		}
		pf := (ssig - ss) / (ssig - 1)
		fearFactor := math.Sqrt(pf*actual*(1-actual)/ss) * 2.58 // 2.58 = z-Critical at 99% confidence

		return actual-fearFactor > target
	}

	tripper := func(cb *circuit.Breaker) bool {
		t.mu.Lock()
		defer t.mu.Unlock()
		tElapsed := time.Since(t.initTime)

		// Initiate Learning Phase
		if t.initTime == (time.Time{}) || tElapsed > t.decisionWindow*time.Duration(relearningCycles) {
			initLearning(cb)
			tElapsed = time.Since(t.initTime)
		}

		cycles := float64(tElapsed) / float64(t.decisionWindow)

		// Terminate Learning Phase
		if t.state == Learning && cycles > learningCycles {
			t.state = Learned
		}

		if t.state == Learning {
			errorRate := recordError(cb)
			failMultiplier := math.Log2(maxFail/t.rate) + 1
			// Trip t.rate starts with t.policies.MaxFail and approaches the Learned Rate * FailMultiplier as learning nears completion
			learnedRateMultiplier := failMultiplier * cycles / learningCycles
			maxFailMultiplier := (learningCycles - cycles) / learningCycles
			tripRate := t.rate*learnedRateMultiplier + maxFail*maxFailMultiplier

			return shouldPerhapsTrip(tripRate, errorRate, cb.Failures()+cb.Successes())
		}

		return shouldPerhapsTrip((1+math.Log2(maxFail/t.rate))*t.rate,
			cb.ErrorRate(),
			cb.Failures()+cb.Successes())
	}

	return tripper
}

// Create a new circuit.Breaker using the dynamically self-configuring SmartTripper
//
// It returns a circuit.Breaker from github.com/rubyist/circuitbreaker
// Please see its documentation to understand how to use the breaker
func NewSmartCircuitBreaker(t *SmartTripper) *circuit.Breaker {
	options := &circuit.Options{
		WindowTime: t.decisionWindow,
	}
	options.ShouldTrip = t.tripFunc()
	return circuit.NewBreakerWithOptions(options)
}
