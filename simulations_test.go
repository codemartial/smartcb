// +build sims

package smartcb_test

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/codemartial/smartcb"
	"github.com/rubyist/circuitbreaker"
)

func SlowRampGenerator(maxRate float64, iters int) func() (error, float64) {
	var i int
	var rate float64
	return func() (error, float64) {
		i, rate = i+1, rate+maxRate/float64(iters)
		if rand.Float64() < rate {
			return fmt.Errorf(""), rate
		}
		return nil, rate
	}
}

func status(tripped bool) string {
	if tripped {
		return "O"
	}
	return "C"
}

func TestSlowRamp(t *testing.T) {
	fmt.Println("Slow Ramp Comparison")
	ticker := time.Tick(time.Microsecond * 10)
	st := smartcb.NewSmartTripper(100000, smartcb.NewPolicies())
	scb := smartcb.NewSmartCircuitBreaker(st)
	rcb := circuit.NewBreakerWithOptions(&circuit.Options{
		ShouldTrip: circuit.RateTripFunc(smartcb.NewPolicies().MaxFail, 100),
		WindowTime: time.Millisecond * 10,
	})
	errgen := SlowRampGenerator(smartcb.NewPolicies().MaxFail*1.2, 1000000)
	var se, re, flipped bool
	for i := 0; i < 1000000; i++ {
		<-ticker
		e, rate := errgen()
		scb.Call(func() error { return e }, 0)
		rcb.Call(func() error { return e }, 0)
		if scb.Tripped() != se {
			se = !se
			flipped = true
		}
		if rcb.Tripped() != re {
			re = !re
			flipped = true
		}
		if flipped || i+1 == 1000000 || i == 10000 {
			fmt.Printf("%6d, %5.2f%%, %5.2f%%, SCB:%s, RCB:%s\n", i, (rate)*100.0, st.LearnedRate()*100.0, status(se), status(re))
			flipped = false
		}
	}
}
func TestAverageJitter(t *testing.T) {
	fmt.Println("Jitter Comparison")
	ticker := time.Tick(time.Microsecond * 10)
	st := smartcb.NewSmartTripper(100000, smartcb.NewPolicies())
	scb := smartcb.NewSmartCircuitBreaker(st)
	rcb := circuit.NewBreakerWithOptions(&circuit.Options{
		ShouldTrip: circuit.RateTripFunc(smartcb.NewPolicies().MaxFail*0.5, 100),
		WindowTime: time.Millisecond * 10,
	})
	errgen := func(rate float64) error {
		if rand.Float64() < rate {
			return fmt.Errorf("")
		}
		return nil
	}
	jitter := 0.0
	rate := smartcb.NewPolicies().MaxFail / 10.0
	var se, re, flipped bool
	for i := 0; i < 1000000; i++ {
		<-ticker
		e := errgen(rate + jitter)
		scb.Call(func() error { return e }, 0)
		rcb.Call(func() error { return e }, 0)
		if scb.Tripped() != se {
			se = !se
			flipped = true
		}
		if rcb.Tripped() != re {
			re = !re
			flipped = true
		}
		if flipped || i+1 == 1000000 || i == 10000 {
			fmt.Printf("%6d, %5.2f%%, %5.2f%%, SCB:%s, RCB:%s\n", i, (rate+jitter)*100.0, st.LearnedRate()*100.0, status(se), status(re))
			flipped = false
		}
		if i > 100000 && i%1000 == 0 {
			jitter = rand.Float64() * rate * 10.0 * float64(500001-i) / 400000.0
			if jitter < 0.0 {
				jitter = 0.0
			}
		}
	}
}
