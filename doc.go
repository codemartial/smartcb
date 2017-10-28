// Package smartcb provides a circuit breaker based on https://github.com/rubyist/circuitbreaker
// that automatically adjusts the tripping error threshold based on abnormal increase
// in error rate. All you need to tell it is the nominal QPS ("queries per second")
// for your task and it automatically sets the best values for adjusting the circuit
// breaker's responsiveness. If you want, you can adjust the circuit breaker's sensitivity
// as per your situation.
//
// The circuit breaker starts off with a learning phase for understanding the error
// profile of the wrapped command and then adjusts its tripping threshold for error
// rate based on what it has learned.
//
// The error threshold is calculated as an exponential weighted moving average which
// smoothens out jitter and can detect rapid changes in error rate, allowing for the
// circuit to trip fast in case of a rapid degradation of the wrapped command.
package smartcb
