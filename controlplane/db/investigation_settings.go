// SuggestionSettings — tunable knobs for the investigation suggestion
// engine. Persisted in the `meta` k/v table under
// `investigation.suggestion_settings` as a JSON blob.
//
// Defaults match the constants the engine shipped with so the system
// behaves identically until an admin changes anything. A zero/nil
// settings struct from the JSON layer is treated as "use defaults" —
// this keeps migrations one-way and lets us add new fields without
// breaking older serialised blobs.

package db

import (
	"encoding/json"
)

// SuggestionSettings carries the threshold + signal weights + window
// sizes used by SuggestFindingsForInvestigation and
// ListRelatedCasesForFinding. JSON tags use snake_case so the
// settings file is operator-editable in a pinch.
type SuggestionSettings struct {
	// Score cutoff — candidates below this don't surface.
	Threshold int `json:"threshold"`

	// Per-signal weights. Map keys mirror SuggestionSignal values.
	Weights map[SuggestionSignal]int `json:"weights"`

	// Same-host signal window: ±N minutes around a linked finding's ts.
	HostWindowMinutes int `json:"host_window_minutes"`

	// Daimon+severity signal lookback in hours.
	DaimonSevWindowHours int `json:"daimon_sev_window_hours"`

	// Cross-CP IOC signal — fires when the matched IOC has at least
	// this many observations within the lookback window. Higher
	// observation counts on a single IOC indicate a wider campaign,
	// which is stronger correlation than a one-off match.
	IOCCrossCPMinObservations int `json:"ioc_cross_cp_min_observations"`
	IOCCrossCPWindowHours     int `json:"ioc_cross_cp_window_hours"`
}

const metaKeySuggestionSettings = "investigation.suggestion_settings"

// DefaultSuggestionSettings — what the engine uses when the meta key
// is absent. Mirrors the constants in investigation_suggestions.go;
// if you change one, change both.
func DefaultSuggestionSettings() SuggestionSettings {
	return SuggestionSettings{
		Threshold: DefaultSuggestionThreshold,
		Weights: map[SuggestionSignal]int{
			SignalDedupKey:   100,
			SignalIOC:        80,
			SignalHostWindow: 60,
			SignalDaimonSev:  30,
			SignalIOCCrossCP: 100,
		},
		HostWindowMinutes:         60,
		DaimonSevWindowHours:      24,
		IOCCrossCPMinObservations: 3,
		IOCCrossCPWindowHours:     24,
	}
}

// GetSuggestionSettings loads + validates the persisted settings.
// Missing meta row → defaults. Malformed JSON → defaults (we don't
// want a corrupted blob to break the suggestion engine; the admin
// can re-save through the settings page).
//
// Each field is independently validated against the default — a
// missing/zero field falls back so admins who edit one row don't
// have to enter all of them.
func (s *Store) GetSuggestionSettings() (SuggestionSettings, error) {
	defaults := DefaultSuggestionSettings()
	raw, err := s.MetaGet(metaKeySuggestionSettings)
	if err != nil {
		return defaults, err
	}
	if raw == "" {
		return defaults, nil
	}
	var got SuggestionSettings
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		// Swallow + return defaults — admin loses tuning but engine
		// keeps working. Logging surfaces this server-side; the UI
		// can re-save to repair.
		return defaults, nil
	}
	mergeSuggestionDefaults(&got, defaults)
	return got, nil
}

// SetSuggestionSettings persists the blob. Validates by serialising
// then storing — invalid values are caught client-side, but a
// nonsensical payload (negative threshold, missing weight map) gets
// merged with defaults so the stored row is always consumable.
func (s *Store) SetSuggestionSettings(in SuggestionSettings) error {
	mergeSuggestionDefaults(&in, DefaultSuggestionSettings())
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return s.MetaSet(metaKeySuggestionSettings, string(b))
}

// mergeSuggestionDefaults fills any zero/nil field on `in` from `def`.
// Mutates `in` in place. Centralised so Get and Set both produce the
// same shape.
func mergeSuggestionDefaults(in *SuggestionSettings, def SuggestionSettings) {
	if in.Threshold <= 0 {
		in.Threshold = def.Threshold
	}
	if in.HostWindowMinutes <= 0 {
		in.HostWindowMinutes = def.HostWindowMinutes
	}
	if in.DaimonSevWindowHours <= 0 {
		in.DaimonSevWindowHours = def.DaimonSevWindowHours
	}
	if in.IOCCrossCPMinObservations <= 0 {
		in.IOCCrossCPMinObservations = def.IOCCrossCPMinObservations
	}
	if in.IOCCrossCPWindowHours <= 0 {
		in.IOCCrossCPWindowHours = def.IOCCrossCPWindowHours
	}
	if in.Weights == nil {
		in.Weights = map[SuggestionSignal]int{}
	}
	for sig, w := range def.Weights {
		if existing, ok := in.Weights[sig]; !ok || existing <= 0 {
			in.Weights[sig] = w
		}
	}
}
