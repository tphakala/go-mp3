//go:build noasm || (!amd64 && !arm64)

package enc

// unpredictability, maskingThresholds, shortThresholds and plogs fall back to
// their pure-Go references on the noasm build and on any architecture without
// a kernel.
func (p *PsyModel) unpredictability() {
	p.unpredictabilityGo()
}

func (p *PsyModel) maskingThresholds() {
	p.maskingThresholdsGo()
}

func (p *PsyModel) shortThresholds(tab *psyShortTable) {
	p.shortThresholdsGo(tab)
}

func plogs(dst, src []float64) {
	plogsGo(dst, src)
}
