package smartcb

import (
	"math"
	"sync"
	"time"

	"github.com/cenk/backoff"
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
	// Default is 0.05 (5%).
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
	MaxFail:           0.05,
	LearningWindowX:   10.0,
	ReLearningWindowX: 100.0,
	EWMADecayFactor:   10.0,
	SamplesPerWindow:  1000,
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

// SmartTripper controls behaviour of the tripping function used
// by the circuit breaker
//
// All circuit breakers obtained out of a SmartTripper instance
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

// NewPolicies returns Policies initialised to default values
//
func NewPolicies() Policies {
	return defaults
}

// NewSmartTripper creates a SmartTripper based on the nominal QPS for your task
//
// "Nominal QPS" is the basis on which the SmartTripper configures its
// responsiveness settings. A suitable value for this parameter would be
// your median QPS. If your QPS varies a lot during operation, choosing this
// value closer to max QPS will make the circuit breaker reluctant to trip
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

// State returns the Learning/Learned status of the Smart Tripper
//
// State change only happens when an error is triggered
// Therefore timing alone can not be relied upon to detect state changes
func (t *SmartTripper) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.state
}

// LearnedRate returns the tripping error rate learned by the SmartTripper
//
func (t *SmartTripper) LearnedRate() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.rate
}

func (t *SmartTripper) initLearning(cb *circuit.Breaker) {
	t.initTime = time.Now()
	t.state = Learning
}

func (t *SmartTripper) decideLearning(cb *circuit.Breaker) {
	learningCycles := t.policies.LearningWindowX
	relearningCycles := t.policies.ReLearningWindowX
	tElapsed := time.Since(t.initTime)

	// Initiate Learning Phase
	if t.initTime == (time.Time{}) || tElapsed > t.decisionWindow*time.Duration(relearningCycles) {
		t.initLearning(cb)
		tElapsed = time.Since(t.initTime)
	}

	cycles := float64(tElapsed) / float64(t.decisionWindow)

	// Terminate Learning Phase
	if t.state == Learning && cycles > learningCycles {
		t.state = Learned
	}
}

func (t *SmartTripper) recordError(cbr, samples float64) bool {
	if t.state != Learning {
		return false
	}

	maxFail := t.policies.MaxFail
	weightage := samples / float64(t.policies.SamplesPerWindow)
	t.rate += (cbr - t.rate) * weightage / (t.policies.EWMADecayFactor + weightage)

	// Enforce learned error rate limits
	if t.rate < minFail {
		t.rate = minFail
	}
	if t.rate > maxFail {
		t.rate = maxFail
	}
	return false
}

// Use Adjusted Wald Method to estimate whether we are confident enough to trip based on the no. of samples
func (t *SmartTripper) shouldPerhapsTrip(target, actual float64, sampleSize int64) bool {
	ss := float64(sampleSize)
	ssig := float64(t.policies.SamplesPerWindow)
	if ss > ssig {
		return actual > target
	}
	pf := (ssig - ss) / (ssig - 1)
	fearFactor := math.Sqrt(pf*actual*(1-actual)/ss) * 2.58 // 2.58 = z-Critical at 99% confidence

	return actual-fearFactor > target
}

func (t *SmartTripper) tripper(cb *circuit.Breaker) bool {
	maxFail := t.policies.MaxFail

	t.mu.Lock()
	defer t.mu.Unlock()

	t.decideLearning(cb)

	samples := cb.Failures() + cb.Successes()
	errorRate := cb.ErrorRate()
	if samples < t.policies.SamplesPerWindow/10 { // Not enough data to decide
		return false
	}

	tripRate := math.Sqrt(maxFail/t.rate) * t.rate
	// Either trip or learn the error rate
	return t.shouldPerhapsTrip(tripRate, errorRate, samples) || t.recordError(errorRate, float64(samples))
}

// NewSmartCircuitBreaker creates a new circuit.Breaker based on the SmartTripper
//
// It returns a circuit.Breaker from github.com/rubyist/circuitbreaker
// Please see its documentation to understand how to use the breaker
func NewSmartCircuitBreaker(t *SmartTripper) *circuit.Breaker {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = t.decisionWindow / 10
	options := &circuit.Options{
		WindowTime: t.decisionWindow,
		BackOff:    bo,
	}
	options.ShouldTrip = t.tripper
	return circuit.NewBreakerWithOptions(options)
}
