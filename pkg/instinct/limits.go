package instinct

// Limits are HowlInstinct's own bounds on the size and shape of a request.
//
// These are deliberately Howl's limits, not a provider's. Upstream
// Jev-compatible endpoints document a cap on choice options and score levels
// but document no cap at all on question count or state size, and limits
// differ between implementations besides. Relying on a remote service to
// bound our resource use would mean an unbounded local footprint whenever
// that service is permissive, misconfigured, or hostile.
type Limits struct {
	// MaxStateBytes bounds the state blob. State is the largest
	// caller-controlled input and the one most likely to be attacker-shaped.
	MaxStateBytes int

	// MaxQuestions bounds a batch. Batching is a feature, but an unbounded
	// batch is a denial-of-service amplifier: one cheap local call becomes an
	// arbitrarily expensive remote one.
	MaxQuestions int

	// MaxInstructionBytes bounds a single question's instructions.
	MaxInstructionBytes int

	// MaxLabelBytes bounds an option or level name and its description.
	MaxLabelBytes int

	// MaxIDBytes bounds a caller-supplied question identifier.
	MaxIDBytes int

	// MaxOptions bounds a choice. The upstream contract documents 255.
	MaxOptions int

	// MinLevels and MaxLevels bound a score scale. The upstream contract
	// documents 2 to 10. Fewer than two levels is not a scale, and a scale
	// wider than the provider supports would be silently truncated.
	MinLevels int
	MaxLevels int

	// MaxResponseBytes bounds a provider response body. A provider is
	// untrusted network input regardless of what it claims about type safety.
	MaxResponseBytes int64
}

// DefaultLimits returns the conservative defaults HowlInstinct ships with.
//
// They are intended to be comfortable for real bounded questions and
// uncomfortable for anything trying to use HowlInstinct as a general-purpose
// text pipe.
func DefaultLimits() Limits {
	return Limits{
		MaxStateBytes:       128 * 1024,
		MaxQuestions:        32,
		MaxInstructionBytes: 4096,
		MaxLabelBytes:       256,
		MaxIDBytes:          64,
		MaxOptions:          255,
		MinLevels:           2,
		MaxLevels:           10,
		MaxResponseBytes:    4 * 1024 * 1024,
	}
}
