package smartcb_test

import (
	"errors"
	"log"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/codemartial/smartcb"
	"github.com/rubyist/circuitbreaker"
)

const minFail = 0.001 // Must match smartcb.minFail (which is unexported)

func Example() {
	// Initialise policies and set max. tolerable failure rate
	// to 15% (= 0.15)
	policies := smartcb.NewPolicies()
	policies.MaxFail = 0.15

	// Create a SmartTripper Generator for a 10k QPS task
	var st = smartcb.NewSmartTripper(10000, policies)
	// Create a Circuit Breaker from the SmartTripper Generator
	var scb = smartcb.NewSmartCircuitBreaker(st)

	// The task to be wrapped with the circuit breaker
	protectedTask := func(errRate float64) (err error) {
		if rand.Float64() < errRate {
			return errors.New("forced error")
		}
		return nil
	}

	breakerEvents := scb.Subscribe()

	// Let's run the example for 200 ms
	stop := time.After(time.Millisecond * 200)
	loop := true
	for loop {
		select {
		case <-stop: // Stop execution now
			loop = false
		case e := <-breakerEvents: // Something changed with the circuit breaker
			if e == circuit.BreakerTripped {
				log.Println("Circuit Breaker tripped.", scb.ErrorRate(), st.State(), st.LearnedRate())
				return
			}
		default: // Execute the task using circuit.Breaker.Call() method
			_ = scb.Call(func() error { return protectedTask(0.02) }, time.Second)
		}
	}
}

func protectedTask(errRate float64) (err error) {
	if rand.Float64() < errRate {
		return errors.New("forced error")
	}
	return nil
}

func TestSmartCB(t *testing.T) {
	st := smartcb.NewSmartTripper(1000, smartcb.NewPolicies())
	scb := smartcb.NewSmartCircuitBreaker(st)

	t.Run("Learning", func(t *testing.T) {
		scb.Reset()
		testStop := time.After(time.Millisecond * 1100)
		bEvents := scb.Subscribe()
		loop := true
		for loop {
			select {
			case <-testStop:
				loop = false
			case e := <-bEvents:
				if e == circuit.BreakerTripped {
					t.Error("Circuit Breaker tripped in Learning Phase.", scb.ErrorRate(), st.State(), st.LearnedRate())
					return
				}
			default:
				_ = scb.Call(func() error { return protectedTask(0.02) }, time.Second)
			}
		}
	})

	t.Run("OperationFail", func(t *testing.T) {
		scb.ResetCounters()
		if st.State() != smartcb.Learned {
			t.Error("Circuit Breaker is still learning")
		}
		for i := 0; i < 1000; i++ {
			err := scb.Call(func() error { return protectedTask(0.2) }, time.Second)
			if err != nil && scb.Tripped() {
				break
			}
		}
		if !scb.Tripped() {
			t.Error("Circuit Breaker did not trip beyond learned failure rate", scb.ErrorRate(), st.LearnedRate())
		}
	})

	t.Run("OperationSuccess", func(t *testing.T) {
		scb.Reset()
		for {
			_ = scb.Call(func() error { return protectedTask(0) }, time.Second)
			if scb.Ready() && scb.Successes() > 50 {
				break
			}
		}
		for j := 0; j < 100; j++ {
			err := scb.Call(func() error { return protectedTask(0.018) }, time.Second)
			if err != nil && scb.Tripped() {
				t.Error(j, "Circuit breaker tripped unexpectedly", scb.ErrorRate())
				return
			}
		}
	})

	t.Run("MaxFail", func(t *testing.T) {
		for i := 0; i < 1000; i++ {
			err := scb.Call(func() error { return protectedTask(smartcb.NewPolicies().MaxFail) }, time.Second)
			if err != nil && scb.Tripped() {
				break
			}
		}
		if !scb.Tripped() {
			t.Error("Circuit Breaker did not trip at max failure rate", scb.ErrorRate(), st.LearnedRate())
		}
	})
}

func TestInvalidDuration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("No panic despite invalid response duration")
		}
	}()

	_ = smartcb.NewSmartTripper(0, smartcb.NewPolicies())
}

// Test that the breaker doesn't learn a high failure rate
func TestLearnGuard(t *testing.T) {
	scb := smartcb.NewSmartCircuitBreaker(smartcb.NewSmartTripper(1000, smartcb.NewPolicies()))
	loop := true
	testStop := time.After(time.Millisecond * 1100)
	var tripped bool
	for loop {
		select {
		case <-testStop:
			loop = false
		default:
			if scb.Call(func() error { return protectedTask(smartcb.NewPolicies().MaxFail * 1.1) }, time.Second) != nil {
				tripped = scb.Tripped()
			}
		}
	}
	if !tripped {
		t.Error("Circuit Breaker learned a dangerous failure rate ", scb.ErrorRate())
	}
}

func TestLowLiquidity(t *testing.T) {
	scb := smartcb.NewSmartCircuitBreaker(smartcb.NewSmartTripper(1000, smartcb.NewPolicies()))
	for i := 0; i < 10; i++ {
		scb.Call(func() error { return protectedTask(0.40) }, time.Second)

		if scb.Tripped() {
			t.Error("Circuit Breaker tripped unreasonably ", scb.ErrorRate(), i)
		}
	}
}

func TestConcurrency(t *testing.T) {
	if testing.Short() {
		return
	}

	st := smartcb.NewSmartTripper(1000, smartcb.NewPolicies())
	scb := smartcb.NewSmartCircuitBreaker(st)
	testStop := time.After(time.Second * 2)
	loop := true
	bEvents := scb.Subscribe()

	concurrentCall := func(errRate float64) {
		var wg sync.WaitGroup
		wg.Add(32) //MAGIC
		for i := 0; i < 32; i++ {
			go func() {
				_ = scb.Call(func() error { return protectedTask(errRate) }, time.Second)
				wg.Done()
			}()
		}
		wg.Wait()
	}

	for loop {
		select {
		case <-testStop:
			loop = false
		case e := <-bEvents:
			if e == circuit.BreakerTripped {
				loop = false
				t.Error("Circuit Breaker tripped in Learning Phase.", scb.ErrorRate(), st.State(), st.LearnedRate())
			}
		default:
			concurrentCall(0.02)
		}
	}

	tripped := false
	testStop = time.After(time.Second * 1)
	loop = true
	for loop {
		select {
		case <-testStop:
			loop = false
		case e := <-bEvents:
			if e == circuit.BreakerTripped {
				tripped = true
			}
		default:
			concurrentCall(0.2)
		}
	}
	if !tripped {
		t.Error("Circuit Breaker didn't trip above learned rate", scb.ErrorRate(), st.LearnedRate())
	}

	testStop = time.After(time.Second * 2)
	loop = true
	for loop {
		select {
		case <-testStop:
			loop = false
		default:
			concurrentCall(0.02)
		}
	}
}

func TestStateLabels(t *testing.T) {
	tests := map[smartcb.State]string{smartcb.Learning: "Learning", smartcb.Learned: "Learned"}
	for k, v := range tests {
		t.Run(v, func(t *testing.T) {
			if k.String() != v {
				t.Error("Invalid Label for Learning State. Expected ", v, ", got ", k.String())
			}
		})
	}
}

func TestZeroErrorLearning(t *testing.T) {
	st := smartcb.NewSmartTripper(10000, smartcb.NewPolicies())
	scb := smartcb.NewSmartCircuitBreaker(st)

	loop := true
	testStop := time.After(time.Millisecond * 110)
	for loop {
		select {
		case <-testStop:
			loop = false
		default:
			if scb.Call(func() error { return protectedTask(minFail / 5.0) }, time.Second) != nil && scb.Tripped() {
				t.Error("Circuit breaker tripped in Learning Phase.", scb.ErrorRate(), st.State(), st.LearnedRate())
			}
		}
	}

	if st.LearnedRate() < minFail {
		t.Error("Circuit breaker learned abnormally low error rate", st.LearnedRate(), "Expected rate was >=", minFail)
	}
}

func TestInitState(t *testing.T) {
	st := smartcb.NewSmartTripper(1000, smartcb.NewPolicies())
	if st.State() == smartcb.Learning {
		t.Error("Circuit breaker initialised in Learning state")
	}
}

func BenchmarkCB(b *testing.B) {
	st := smartcb.NewSmartTripper(100000, smartcb.NewPolicies())
	scb := smartcb.NewSmartCircuitBreaker(st)
	bEvents := scb.Subscribe()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			scb.Call(func() error { return protectedTask(0.0) }, time.Second)
			select {
			case e := <-bEvents:
				if e == circuit.BreakerTripped || e == circuit.BreakerFail {
					b.Error("Circuit breaker failed")
				}
			default:
			}
		}
	})
}
