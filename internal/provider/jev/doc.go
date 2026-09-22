// Package jev adapts HowlInstinct to any endpoint speaking the
// Jev-compatible System One contract: POST /v1/systemone with typed noul,
// choice, and score questions.
//
// The adapter is generic over the endpoint. OpenJev, a hosted Jev service, a
// local server, or any future compatible implementation are all reached by
// configuration alone; no hostname, model name, vendor, or hardware
// assumption is compiled in. This package is the only place in HowlInstinct
// where a wire format exists at all.
//
// Everything arriving from the endpoint is treated as untrusted input. That a
// response came from a service advertising type-safe structured output is not
// evidence that it is well formed, in range, internally consistent, or even
// answering the question that was asked. Every response therefore goes
// through the validation pipeline in validate.go before a single value is
// allowed into a judgment.
package jev
