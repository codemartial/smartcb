package smartcb_test

import (
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/codemartial/smartcb"
)

var scb = smartcb.New(10000)

func protectedTask(errRate float64) (err error) {
	if rand.Float64() < errRate {
		return errors.New("forced error")
	}
	return nil
}

func TestSmartCBLearning(t *testing.T) {
	testStop := time.After(time.Millisecond * 1100)
	loop := true
	for loop {
		select {
		case <-testStop:
			loop = false
		default:
			err := scb.Call(func() error { return protectedTask(0.02) }, time.Second)
			if err != nil && scb.Tripped() {
				t.Error("Circuit Breaker tripped in Learning Phase. Error Rate:", scb.ErrorRate())
				return
			}
		}
	}
}

func TestSmartCBOperationFail(t *testing.T) {
	scb.ResetCounters()
	for i := 0; i < 1000; i++ {
		err := scb.Call(func() error { return protectedTask(0.2) }, time.Second)
		if err != nil && scb.Tripped() {
			break
		}
	}
	if !scb.Tripped() {
		t.Error("Circuit Breaker did not trip beyond learned failure rate", scb.ErrorRate())
	}
}

func TestCBOperationSuccess(t *testing.T) {
	scb.Reset()
	var i = 0
	for ; ; i++ {
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
}
func TestInvalidDuration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("No panic despite invalid response duration")
		}
	}()

	_ = smartcb.New(0)
}

func TestOverFailLearnGuard(t *testing.T) {
	scb = smartcb.New(10000)
	loop := true
	testStop := time.After(time.Millisecond * 1100)
	var tripped bool
	for loop {
		select {
		case <-testStop:
			loop = false
		default:
			if scb.Call(func() error { return protectedTask(0.41) }, time.Second) != nil {
				tripped = scb.Tripped()
			}
		}
	}
	if !tripped {
		t.Error("Circuit Breaker learned a dangerous failure rate ", scb.ErrorRate())
	}
	t.Log(scb.ErrorRate())
}

func TestLongRun(t *testing.T) {
	scb = smartcb.New(10000)
	ticker := time.Tick(time.Microsecond * 100)
	testStop := time.After(time.Second * 2)
	prevState := false
	loop := true
	start := time.Now()
	for loop {
		select {
		case <-testStop:
			loop = false
		default:
			_ = scb.Call(func() error { <-ticker; return protectedTask(0.02) }, time.Second)
			if scb.Tripped() != prevState {
				prevState = !prevState
				t.Log(time.Since(start), prevState, scb.ErrorRate())
			}
		}
	}

	testStop = time.After(time.Second * 1)
	loop = true
	for loop {
		select {
		case <-testStop:
			loop = false
		default:
			_ = scb.Call(func() error { <-ticker; return protectedTask(0.2) }, time.Second)
			if scb.Tripped() != prevState {
				prevState = !prevState
				t.Log(time.Since(start), prevState, scb.ErrorRate())
			}
		}
	}

	testStop = time.After(time.Second * 2)
	loop = true
	for loop {
		select {
		case <-testStop:
			loop = false
		default:
			_ = scb.Call(func() error { <-ticker; return protectedTask(0.02) }, time.Second)
			if scb.Tripped() != prevState {
				prevState = !prevState
				t.Log(time.Since(start), prevState, scb.ErrorRate())
			}
		}
	}
}
