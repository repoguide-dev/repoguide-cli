package session

func computeContextStats(events []SessionEvent, usage *TokenUsage) *ContextStats {
	var maxEff, sumEff, maxEffBeforeFirstEdit int64
	var effCount int64
	firstEditSeen := false

	for _, event := range events {
		if len(event.WritePaths) > 0 {
			firstEditSeen = true
		}
		if event.Kind != "token_usage" || event.TokenUsage == nil || event.TokenUsage.Cumulative {
			continue
		}
		eff := effectiveInputTokens(*event.TokenUsage)
		if eff > maxEff {
			maxEff = eff
		}
		sumEff += eff
		effCount++
		if !firstEditSeen && eff > maxEffBeforeFirstEdit {
			maxEffBeforeFirstEdit = eff
		}
	}

	if usage == nil {
		return nil
	}

	var avg float64
	if effCount > 0 {
		avg = float64(sumEff) / float64(effCount)
	}

	denominator := usage.InputTokens + usage.CacheReadTokens
	var cacheHitRatio float64
	if denominator > 0 {
		cacheHitRatio = float64(usage.CacheReadTokens) / float64(denominator)
	}

	return &ContextStats{
		MaxEffectiveInputTokens:    maxEff,
		AvgEffectiveInputTokens:    avg,
		PeakContextBeforeFirstEdit: maxEffBeforeFirstEdit,
		CacheHitRatio:              cacheHitRatio,
		ContextPressure:            contextPressureLabel(maxEff),
		CacheReuse:                 cacheReuseLabel(cacheHitRatio),
	}
}

func contextPressureLabel(maxEff int64) string {
	switch {
	case maxEff <= 0:
		return ""
	case maxEff < 50_000:
		return "Low"
	case maxEff < 120_000:
		return "Medium"
	case maxEff < 180_000:
		return "High"
	default:
		return "Critical"
	}
}

func cacheReuseLabel(ratio float64) string {
	switch {
	case ratio <= 0:
		return "None"
	case ratio < 0.2:
		return "Low"
	case ratio < 0.5:
		return "Moderate"
	case ratio < 0.8:
		return "High"
	default:
		return "Very high"
	}
}
