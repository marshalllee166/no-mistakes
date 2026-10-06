package config

import "fmt"

const (
	// PRAppendixFull publishes Intent (when enabled), Risk Assessment, Testing,
	// and Pipeline as separate sections. It is the default.
	PRAppendixFull = "full"
	// PRAppendixCollapsed folds Risk Assessment, Testing, and Pipeline into one
	// closed Validation details block. Intent stays outside that block.
	PRAppendixCollapsed = "collapsed"
	// PRAppendixMinimal publishes a one-line risk level and the pipeline
	// attestation, and omits Testing and Pipeline prose.
	PRAppendixMinimal = "minimal"
	// PRAppendixNone publishes no visible appendix: only the pipeline
	// attestation marker, hidden where the host renders HTML comments.
	PRAppendixNone = "none"
)

// AppendixMode reports the publication mode. An empty value is full, matching
// repositories that have never set pr.appendix. Validation rejects every other
// string at parse time.
func (p PR) AppendixMode() string {
	switch p.Appendix {
	case PRAppendixCollapsed, PRAppendixMinimal, PRAppendixNone, PRAppendixFull:
		return p.Appendix
	default:
		return PRAppendixFull
	}
}

func validatePRAppendix(mode string) error {
	switch mode {
	case "", PRAppendixFull, PRAppendixCollapsed, PRAppendixMinimal, PRAppendixNone:
		return nil
	default:
		return fmt.Errorf("pr.appendix: %q is not a valid mode (want %q, %q, %q, or %q)", mode, PRAppendixFull, PRAppendixCollapsed, PRAppendixMinimal, PRAppendixNone)
	}
}

func validateOperatorPR(pr OperatorPRRaw) error {
	if pr.Appendix == nil {
		return nil
	}
	if *pr.Appendix == "" {
		return fmt.Errorf("pr.appendix must not be empty")
	}
	return validatePRAppendix(*pr.Appendix)
}
