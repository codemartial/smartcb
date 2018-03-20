// +build sims

package smartcb_test

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codemartial/smartcb"
	"github.com/eapache/go-resiliency/breaker"
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
func TestThrottle(t *testing.T) {
	testRealWorld(t, false)
}

func TestTotalFailure(t *testing.T) {
	testRealWorld(t, true)
}

func flaky(iter int, ratelimiter chan struct{}) error {
	time.Sleep(time.Millisecond*100 + time.Millisecond*time.Duration(rand.Float64())*100)
	if iter < 50000 || iter > 70000 {
		return nil
	}
	select {
	case <-ratelimiter:
		return nil
	default:
		time.Sleep(time.Millisecond*1000 + time.Millisecond*time.Duration(rand.Float64())*1000)
		return fmt.Errorf("Rate Limit Exceeded")
	}
}

func testRealWorld(t *testing.T, total bool) {
	fmt.Println("Throttle Sim")
	ticker := time.Tick(time.Microsecond * 2500)
	st := smartcb.NewSmartTripper(100, smartcb.NewPolicies())
	scb := smartcb.NewSmartCircuitBreaker(st)
	rcb := circuit.NewThresholdBreaker(20)
	gcb := breaker.New(10, 2, 2*time.Second)

	ratelimiter := make(chan struct{}, 3)
	errgen := func(i int) error { return flaky(i, ratelimiter) }

	go func() {
		if total {
			return
		}
		ticks := time.Tick(time.Microsecond * 6667)
		for range ticks {
			for i := 0; i < 3; i++ {
				select {
				case ratelimiter <- struct{}{}:
				default:
				}
			}
		}
	}()

	var wg sync.WaitGroup
	var attempts_scb, attempts_rcb, attempts_gcb int64
	for i := 0; i < 100000; i++ {
		<-ticker
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			if scb.Ready() {
				atomic.AddInt64(&attempts_scb, 1)
				if errgen(i) == nil {
					scb.Success()
				} else {
					scb.Fail()
				}
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			if rcb.Ready() {
				atomic.AddInt64(&attempts_rcb, 1)
				if errgen(i) == nil {
					rcb.Success()
				} else {
					rcb.Fail()
				}
			}
		}(i)
		go func(i int) {
			defer wg.Done()

			if cberr := gcb.Run(func() error { return errgen(i) }); cberr != breaker.ErrBreakerOpen {
				atomic.AddInt64(&attempts_gcb, 1)
			}
		}(i)
		if i%5000 == 0 {
			fmt.Println(i, atomic.LoadInt64(&attempts_scb), atomic.LoadInt64(&attempts_rcb), atomic.LoadInt64(&attempts_gcb))
		}
	}
	wg.Wait()
	fmt.Println(99999, atomic.LoadInt64(&attempts_scb), atomic.LoadInt64(&attempts_rcb), atomic.LoadInt64(&attempts_gcb))
	fmt.Println("Done")
}
