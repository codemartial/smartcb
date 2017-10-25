package smartcb_test

import (
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/codemartial/smartcb"
)

var cb = smartcb.New(time.Millisecond * 100)

func protectedTask(errRate float64) (err error) {
	t := time.After(time.Millisecond)
	err = nil
	if rand.Float64() < errRate {
		err = errors.New("forced error")
	}
	<-t
	return
}

func TestInvalidDuration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("No panic despite invalid response duration")
		}
	}()

	_ = smartcb.New(0)
}
func TestSmartCBLearning(t *testing.T) {
	testStop := time.After(time.Millisecond * 500)
	learningStop := time.After(time.Second)
	for i, loop := 0, true; !loop; i++ {
		select {
		case <-testStop:
			loop = false
		default:
			err := cb.Call(func() error { return protectedTask(0.2) }, time.Second)
			if err != nil && cb.Tripped() {
				t.Error("Circuit Breaker tripped in Learning Phase. Iteration:", i, "Error Rate:", cb.ErrorRate())
				return
			}
		}
	}
	<-learningStop
}

func TestSmartCBOperationFail(t *testing.T) {
	for i := 0; i < 1000; i++ {
		_ = cb.Call(func() error { return protectedTask(0.25) }, time.Second)
	}
	if !cb.Tripped() {
		t.Error("Circuit Breaker did not trip beyond learned failure rate", cb.ErrorRate())
	}
}

func TestCBOperationSuccess(t *testing.T) {
	cb.Reset()
	var i = 0
	for ; ; i++ {
		_ = cb.Call(func() error { return protectedTask(0) }, time.Second)
		if cb.Ready() && cb.Successes() > 50 {
			break
		}
	}
	for j := 0; j < 100; j++ {
		err := cb.Call(func() error { return protectedTask(0.18) }, time.Second)
		if err != nil && cb.Tripped() {
			t.Error(j, "Circuit breaker tripped unexpectedly", cb.ErrorRate())
			return
		}
	}
}
