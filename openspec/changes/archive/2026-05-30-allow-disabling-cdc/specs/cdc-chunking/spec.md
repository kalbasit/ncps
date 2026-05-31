## MODIFIED Requirements

### Requirement: CDC startup validation MUST allow enabled→disabled transition

When CDC configuration is validated at startup via `ValidateOrStoreCDCConfig`, the system SHALL permit the transition from a stored `cdc_enabled=true` to a current `enabled=false`. The previous behavior of returning `ErrCDCDisabledAfterEnabled` for this transition is removed.

The updated validation rules are:
- If no stored CDC config exists and `enabled=false`: no-op, return nil.
- If no stored CDC config exists and `enabled=true`: store the new config (first boot), return nil.
- If stored config exists and `enabled=true`: validate that all four stored values match current values; return error on mismatch.
- If stored config exists and `enabled=false` (enabled→disabled transition): clear all four stored CDC config keys (`cdc_enabled`, `cdc_min`, `cdc_avg`, `cdc_max`); return nil.

#### Scenario: Disabling CDC after being enabled clears stored config and succeeds

- **GIVEN** `cdc_enabled=true` is stored in the configuration database
- **WHEN** `ValidateOrStoreCDCConfig` is called with `enabled=false`
- **THEN** it SHALL return nil (no error)
- **AND** the configuration database SHALL have no entry for `cdc_enabled`, `cdc_min`, `cdc_avg`, or `cdc_max`

#### Scenario: Keeping CDC enabled with matching config succeeds

- **GIVEN** `cdc_enabled=true` is stored with matching min/avg/max values
- **WHEN** `ValidateOrStoreCDCConfig` is called with `enabled=true` and the same sizes
- **THEN** it SHALL return nil

#### Scenario: Keeping CDC enabled with mismatched sizes fails

- **GIVEN** `cdc_enabled=true` is stored with `cdc_min=16384`
- **WHEN** `ValidateOrStoreCDCConfig` is called with `enabled=true` and `minSize=32768`
- **THEN** it SHALL return a non-nil error describing the mismatch
