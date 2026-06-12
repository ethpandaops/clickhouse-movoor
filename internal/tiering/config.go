// Package tiering implements movoor's partition tiering controller.
package tiering

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultTargetDisk                         = "s3_cache"
	DefaultInterval                           = 5 * time.Minute
	DefaultMaxConcurrentPartitions            = 1
	DefaultMaxConcurrentObservations          = 4
	DefaultMaxMovesPerCycle                   = 4
	DefaultMaxBytesInFlight            uint64 = 500 << 30
	DefaultMaxBytesPerDay              uint64 = 2 << 40
	DefaultDiffBreakerMaxPartitions           = 50
	DefaultDiffBreakerMaxTableFraction        = 0.5
	DefaultQuietFor                           = 24 * time.Hour
	DefaultTierFrozenAfter                    = 30 * 24 * time.Hour
	DefaultOptimizeToParts             uint64 = 1
	DefaultOptimizeSkipAboveBytes      uint64 = 300 << 30
	DefaultOptimizeStallAfter                 = 30 * time.Minute
	DefaultResplitQuietFor                    = 7 * 24 * time.Hour
	DefaultResplitFragmentParts        uint64 = 6
)

type Mode string

const (
	ModeOff     Mode = "off"
	ModePlan    Mode = "plan"
	ModeEnforce Mode = "enforce"
)

type AgeBasis string

const (
	AgeBasisPartitionTime AgeBasis = "partitionTime"
	AgeBasisFrontier      AgeBasis = "frontier"
)

type ResplitStrategy string

const (
	ResplitStrategyAuto    ResplitStrategy = "auto"
	ResplitStrategyRemerge ResplitStrategy = "remerge"
	ResplitStrategyAppend  ResplitStrategy = "append"
	ResplitStrategyHold    ResplitStrategy = "hold"
)

type SealedSignal string

const (
	SealedSignalAuto             SealedSignal = "auto"
	SealedSignalPartLog          SealedSignal = "partLog"
	SealedSignalModificationTime SealedSignal = "modificationTime"
)

// OptimizeSide selects where the optimize leg runs, which in turn decides
// which direction a split partition's parts move to co-locate before the
// merge: hot pulls cold strays back (the round-trip), cold appends hot strays
// and merges in place. Cold-side merges run at the target disk's speed and
// should only be used where FINAL merges are acceptable.
type OptimizeSide string

const (
	OptimizeOnHot  OptimizeSide = "hot"
	OptimizeOnCold OptimizeSide = "cold"
)

type Duration struct {
	time.Duration
}

type Bytes struct {
	Value uint64
}

//nolint:tagliatelle // Tiering YAML intentionally uses documented camelCase keys.
type Config struct {
	Mode     Mode     `yaml:"mode"`
	Interval Duration `yaml:"interval"`
	// MaxConcurrentPartitions caps concurrently executing legs PER NODE —
	// nodes own independent disks, so the slot pools are independent;
	// Safety.MaxBytesInFlight is the aggregate cross-node ceiling.
	MaxConcurrentPartitions int `yaml:"maxConcurrentPartitions"`
	// MaxConcurrentObservations caps concurrent table observations PER NODE.
	// Every watch runs its own reconcile loop; without a bound, hundreds of
	// loops fire their multi-query observation pipelines at the same node
	// simultaneously on startup and all of them blow the shared query
	// timeout together.
	MaxConcurrentObservations int          `yaml:"maxConcurrentObservations"`
	Safety                    SafetyConfig `yaml:"safety"`
	Defaults                  TierSettings `yaml:"defaults"`
}

//nolint:tagliatelle // Tiering YAML intentionally uses documented camelCase keys.
type SafetyConfig struct {
	MaxMovesPerCycle  int               `yaml:"maxMovesPerCycle"`
	MaxBytesInFlight  Bytes             `yaml:"maxBytesInFlight"`
	MaxBytesPerDay    Bytes             `yaml:"maxBytesPerDay"`
	PauseAfterActions int               `yaml:"pauseAfterActions"`
	DiffBreaker       DiffBreakerConfig `yaml:"diffBreaker"`
}

//nolint:tagliatelle // Tiering YAML intentionally uses documented camelCase keys.
type DiffBreakerConfig struct {
	MaxPartitions    int                  `yaml:"maxPartitions"`
	MaxTableFraction float64              `yaml:"maxTableFraction"`
	Override         *DiffBreakerOverride `yaml:"override"`
}

//nolint:tagliatelle // Tiering YAML intentionally uses documented camelCase keys.
type DiffBreakerOverride struct {
	MaxPartitions    int       `yaml:"maxPartitions"`
	MaxTableFraction float64   `yaml:"maxTableFraction"`
	Expires          time.Time `yaml:"expires"`
}

//nolint:tagliatelle // Tiering YAML intentionally uses documented camelCase keys.
type TierSettings struct {
	Mode                   Mode            `yaml:"mode"`
	TargetDisk             string          `yaml:"targetDisk"`
	HotVolume              string          `yaml:"hotVolume"`
	QuietFor               Duration        `yaml:"quietFor"`
	SealedSignal           SealedSignal    `yaml:"sealedSignal"`
	TierFrozenAfter        Duration        `yaml:"tierFrozenAfter"`
	OptimizeToParts        uint64          `yaml:"optimizeToParts"`
	SkipOptimize           bool            `yaml:"skipOptimize"`
	OptimizeOn             OptimizeSide    `yaml:"optimizeOn"`
	OptimizeSkipAboveBytes Bytes           `yaml:"optimizeSkipAboveBytes"`
	OptimizeStallAfter     Duration        `yaml:"optimizeStallAfter"`
	Resplit                ResplitSettings `yaml:"resplit"`
	Age                    AgeSettings     `yaml:"age"`
	ExcludePartitions      []string        `yaml:"excludePartitions"`
}

//nolint:tagliatelle // Tiering YAML intentionally uses documented camelCase keys.
type AgeSettings struct {
	Basis     AgeBasis `yaml:"basis"`
	OlderThan Duration `yaml:"olderThan"`
	Field     string   `yaml:"field"`
	KeepLast  uint64   `yaml:"keepLast"`
}

//nolint:tagliatelle // Tiering YAML intentionally uses documented camelCase keys.
type ResplitSettings struct {
	Strategy               ResplitStrategy `yaml:"strategy"`
	QuietFor               Duration        `yaml:"quietFor"`
	FragmentAbovePartCount uint64          `yaml:"fragmentAbovePartCount"`
}

type EffectiveWatch struct {
	Database string
	Table    string
	Settings *TierSettings
}

func DefaultConfig() Config {
	return Config{
		Mode:                      ModePlan,
		Interval:                  Duration{Duration: DefaultInterval},
		MaxConcurrentPartitions:   DefaultMaxConcurrentPartitions,
		MaxConcurrentObservations: DefaultMaxConcurrentObservations,
		Safety: SafetyConfig{
			MaxMovesPerCycle: DefaultMaxMovesPerCycle,
			MaxBytesInFlight: Bytes{Value: DefaultMaxBytesInFlight},
			MaxBytesPerDay:   Bytes{Value: DefaultMaxBytesPerDay},
			DiffBreaker: DiffBreakerConfig{
				MaxPartitions:    DefaultDiffBreakerMaxPartitions,
				MaxTableFraction: DefaultDiffBreakerMaxTableFraction,
			},
		},
		Defaults: DefaultTierSettings(),
	}
}

func DefaultTierSettings() TierSettings {
	return TierSettings{
		TargetDisk:             DefaultTargetDisk,
		QuietFor:               Duration{Duration: DefaultQuietFor},
		SealedSignal:           SealedSignalAuto,
		TierFrozenAfter:        Duration{Duration: DefaultTierFrozenAfter},
		OptimizeToParts:        DefaultOptimizeToParts,
		OptimizeOn:             OptimizeOnHot,
		OptimizeSkipAboveBytes: Bytes{Value: DefaultOptimizeSkipAboveBytes},
		OptimizeStallAfter:     Duration{Duration: DefaultOptimizeStallAfter},
		Resplit: ResplitSettings{
			Strategy:               ResplitStrategyRemerge,
			QuietFor:               Duration{Duration: DefaultResplitQuietFor},
			FragmentAbovePartCount: DefaultResplitFragmentParts,
		},
	}
}

func (c *Config) ResolveDefaults() {
	defaults := DefaultConfig()
	if c.Mode == "" {
		c.Mode = defaults.Mode
	}
	if c.Interval.Duration == 0 {
		c.Interval = defaults.Interval
	}
	if c.MaxConcurrentPartitions == 0 {
		c.MaxConcurrentPartitions = defaults.MaxConcurrentPartitions
	}
	if c.MaxConcurrentObservations == 0 {
		c.MaxConcurrentObservations = defaults.MaxConcurrentObservations
	}
	if c.Safety.MaxMovesPerCycle == 0 {
		c.Safety.MaxMovesPerCycle = defaults.Safety.MaxMovesPerCycle
	}
	if c.Safety.MaxBytesInFlight.Value == 0 {
		c.Safety.MaxBytesInFlight = defaults.Safety.MaxBytesInFlight
	}
	if c.Safety.MaxBytesPerDay.Value == 0 {
		c.Safety.MaxBytesPerDay = defaults.Safety.MaxBytesPerDay
	}
	if c.Safety.DiffBreaker.MaxPartitions == 0 {
		c.Safety.DiffBreaker.MaxPartitions = defaults.Safety.DiffBreaker.MaxPartitions
	}
	if c.Safety.DiffBreaker.MaxTableFraction == 0 {
		c.Safety.DiffBreaker.MaxTableFraction = defaults.Safety.DiffBreaker.MaxTableFraction
	}
	c.Defaults.ResolveDefaults()
}

func (s *TierSettings) ResolveDefaults() {
	defaults := DefaultTierSettings()
	if s.TargetDisk == "" {
		s.TargetDisk = defaults.TargetDisk
	}
	if s.QuietFor.Duration == 0 {
		s.QuietFor = defaults.QuietFor
	}
	if s.SealedSignal == "" {
		s.SealedSignal = defaults.SealedSignal
	}
	if s.TierFrozenAfter.Duration == 0 {
		s.TierFrozenAfter = defaults.TierFrozenAfter
	}
	if s.OptimizeToParts == 0 {
		s.OptimizeToParts = defaults.OptimizeToParts
	}
	if s.OptimizeOn == "" {
		s.OptimizeOn = defaults.OptimizeOn
	}
	if s.OptimizeSkipAboveBytes.Value == 0 {
		s.OptimizeSkipAboveBytes = defaults.OptimizeSkipAboveBytes
	}
	if s.OptimizeStallAfter.Duration == 0 {
		s.OptimizeStallAfter = defaults.OptimizeStallAfter
	}
	if s.Resplit.Strategy == "" {
		s.Resplit.Strategy = defaults.Resplit.Strategy
	}
	if s.Resplit.QuietFor.Duration == 0 {
		s.Resplit.QuietFor = defaults.Resplit.QuietFor
	}
	if s.Resplit.FragmentAbovePartCount == 0 {
		s.Resplit.FragmentAbovePartCount = defaults.Resplit.FragmentAbovePartCount
	}
}

func (s TierSettings) Clone() TierSettings {
	clone := s
	if s.ExcludePartitions != nil {
		clone.ExcludePartitions = append([]string(nil), s.ExcludePartitions...)
	}
	return clone
}

func (c Config) Validate(now time.Time) error {
	if err := c.Mode.Validate("tiering.mode"); err != nil {
		return err
	}
	if c.Interval.Duration <= 0 {
		return errors.New("tiering.interval must be positive")
	}
	if c.MaxConcurrentPartitions <= 0 {
		return errors.New("tiering.maxConcurrentPartitions must be positive")
	}
	if c.MaxConcurrentObservations <= 0 {
		return errors.New("tiering.maxConcurrentObservations must be positive")
	}
	if c.Safety.MaxMovesPerCycle <= 0 {
		return errors.New("tiering.safety.maxMovesPerCycle must be positive")
	}
	if c.Safety.MaxBytesInFlight.Value == 0 {
		return errors.New("tiering.safety.maxBytesInFlight must be positive")
	}
	if c.Safety.MaxBytesPerDay.Value == 0 {
		return errors.New("tiering.safety.maxBytesPerDay must be positive")
	}
	if c.Safety.DiffBreaker.MaxPartitions <= 0 {
		return errors.New("tiering.safety.diffBreaker.maxPartitions must be positive")
	}
	if c.Safety.DiffBreaker.MaxTableFraction <= 0 || c.Safety.DiffBreaker.MaxTableFraction > 1 {
		return errors.New("tiering.safety.diffBreaker.maxTableFraction must be in (0, 1]")
	}
	if override := c.Safety.DiffBreaker.Override; override != nil {
		if override.MaxPartitions <= 0 {
			return errors.New("tiering.safety.diffBreaker.override.maxPartitions must be positive")
		}
		if override.MaxTableFraction <= 0 || override.MaxTableFraction > 1 {
			return errors.New("tiering.safety.diffBreaker.override.maxTableFraction must be in (0, 1]")
		}
		if !override.Expires.After(now) {
			return errors.New("tiering.safety.diffBreaker.override.expires must be in the future")
		}
	}
	if len(c.Defaults.ExcludePartitions) > 0 {
		return errors.New("tiering.defaults.excludePartitions is not meaningful; configure exclusions on watches[].tier")
	}
	if err := c.Defaults.Validate("tiering.defaults", false); err != nil {
		return err
	}
	return nil
}

func (s TierSettings) Validate(path string, requireAge bool) error {
	if s.Mode != "" {
		if err := s.Mode.Validate(path + ".mode"); err != nil {
			return err
		}
	}
	if s.TargetDisk == "" {
		return fmt.Errorf("%s.targetDisk is required", path)
	}
	if s.QuietFor.Duration <= 0 {
		return fmt.Errorf("%s.quietFor must be positive", path)
	}
	if err := s.SealedSignal.Validate(path + ".sealedSignal"); err != nil {
		return err
	}
	if s.TierFrozenAfter.Duration < s.QuietFor.Duration {
		return fmt.Errorf("%s.tierFrozenAfter must be greater than or equal to %s.quietFor", path, path)
	}
	if s.OptimizeToParts == 0 {
		return fmt.Errorf("%s.optimizeToParts must be at least 1; use skipOptimize: true to disable optimize", path)
	}
	if err := s.OptimizeOn.Validate(path + ".optimizeOn"); err != nil {
		return err
	}
	if s.OptimizeOn == OptimizeOnCold && s.SkipOptimize {
		return fmt.Errorf("%s.optimizeOn: cold is meaningless with skipOptimize: true", path)
	}
	if s.OptimizeStallAfter.Duration <= 0 {
		return fmt.Errorf("%s.optimizeStallAfter must be positive", path)
	}
	if err := s.Resplit.Validate(path+".resplit", s.QuietFor.Duration); err != nil {
		return err
	}
	if err := s.Age.Validate(path+".age", requireAge); err != nil {
		return err
	}
	for i, partition := range s.ExcludePartitions {
		if strings.TrimSpace(partition) == "" {
			return fmt.Errorf("%s.excludePartitions[%d] must not be empty", path, i)
		}
		if err := ValidatePartitionLiteralSyntax(partition); err != nil {
			return fmt.Errorf("%s.excludePartitions[%d]: %w", path, i, err)
		}
	}
	return nil
}

func (s ResplitSettings) Validate(path string, quietFor time.Duration) error {
	if err := s.Strategy.Validate(path + ".strategy"); err != nil {
		return err
	}
	if s.QuietFor.Duration < quietFor {
		return fmt.Errorf("%s.quietFor must be greater than or equal to quietFor", path)
	}
	if s.FragmentAbovePartCount == 0 {
		return fmt.Errorf("%s.fragmentAbovePartCount must be positive", path)
	}
	return nil
}

func (a AgeSettings) Validate(path string, required bool) error {
	if a.Basis == "" {
		if required {
			return fmt.Errorf("%s.basis is required", path)
		}
		return nil
	}
	if err := a.Basis.Validate(path + ".basis"); err != nil {
		return err
	}
	switch a.Basis {
	case AgeBasisPartitionTime:
		if a.OlderThan.Duration <= 0 {
			return fmt.Errorf("%s.olderThan must be positive for partitionTime", path)
		}
	case AgeBasisFrontier:
		if a.Field == "" {
			return fmt.Errorf("%s.field is required for frontier", path)
		}
		if a.KeepLast == 0 {
			return fmt.Errorf("%s.keepLast must be positive for frontier", path)
		}
	}
	return nil
}

func (m Mode) Validate(path string) error {
	switch m {
	case ModeOff, ModePlan, ModeEnforce:
		return nil
	default:
		return fmt.Errorf("%s must be one of off, plan, or enforce", path)
	}
}

func (b AgeBasis) Validate(path string) error {
	switch b {
	case AgeBasisPartitionTime, AgeBasisFrontier:
		return nil
	default:
		return fmt.Errorf("%s must be one of partitionTime or frontier", path)
	}
}

func (s ResplitStrategy) Validate(path string) error {
	switch s {
	case ResplitStrategyAuto, ResplitStrategyRemerge, ResplitStrategyAppend, ResplitStrategyHold:
		return nil
	default:
		return fmt.Errorf("%s must be one of auto, remerge, append, or hold", path)
	}
}

func (s SealedSignal) Validate(path string) error {
	switch s {
	case SealedSignalAuto, SealedSignalPartLog, SealedSignalModificationTime:
		return nil
	default:
		return fmt.Errorf("%s must be one of auto, partLog, or modificationTime", path)
	}
}

func (o OptimizeSide) Validate(path string) error {
	switch o {
	case OptimizeOnHot, OptimizeOnCold:
		return nil
	default:
		return fmt.Errorf("%s must be one of hot or cold", path)
	}
}

func (m *Mode) UnmarshalYAML(value *yaml.Node) error {
	*m = Mode(value.Value)
	return m.Validate(fmt.Sprintf("line %d", value.Line))
}

func (b *AgeBasis) UnmarshalYAML(value *yaml.Node) error {
	*b = AgeBasis(value.Value)
	return b.Validate(fmt.Sprintf("line %d", value.Line))
}

func (s *ResplitStrategy) UnmarshalYAML(value *yaml.Node) error {
	*s = ResplitStrategy(value.Value)
	return s.Validate(fmt.Sprintf("line %d", value.Line))
}

func (s *SealedSignal) UnmarshalYAML(value *yaml.Node) error {
	*s = SealedSignal(value.Value)
	return s.Validate(fmt.Sprintf("line %d", value.Line))
}

func (o *OptimizeSide) UnmarshalYAML(value *yaml.Node) error {
	*o = OptimizeSide(value.Value)
	return o.Validate(fmt.Sprintf("line %d", value.Line))
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Tag == "!!null" {
		return nil
	}
	parsed, err := ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", value.Line, err)
	}
	*d = Duration{Duration: parsed}
	return nil
}

func (b *Bytes) UnmarshalYAML(value *yaml.Node) error {
	if value.Tag == "!!null" {
		return nil
	}
	parsed, err := ParseBytes(value.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", value.Line, err)
	}
	*b = Bytes{Value: parsed}
	return nil
}

var durationToken = regexp.MustCompile(`^([0-9]+)(ms|s|m|h|d|w|y)`)

var durationUnits = map[string]time.Duration{
	"ms": time.Millisecond,
	"s":  time.Second,
	"m":  time.Minute,
	"h":  time.Hour,
	"d":  24 * time.Hour,
	"w":  7 * 24 * time.Hour,
	"y":  365 * 24 * time.Hour,
}

// ParseDuration accepts the prometheus model.Duration grammar: one or more
// `<int><unit>` tokens (`ms s m h d w y`), so both `35d` and compound forms
// like `1h30m` parse. `1d`=24h, `1w`=7d, `1y`=365d.
func ParseDuration(raw string) (time.Duration, error) {
	remaining := strings.TrimSpace(raw)
	if remaining == "" {
		return 0, fmt.Errorf("duration %q must use ms, s, m, h, d, w, or y", raw)
	}
	var total time.Duration
	for remaining != "" {
		match := durationToken.FindStringSubmatch(remaining)
		if match == nil {
			return 0, fmt.Errorf("duration %q must use ms, s, m, h, d, w, or y", raw)
		}
		value, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || value < 0 {
			return 0, fmt.Errorf("duration %q is invalid", raw)
		}
		total += time.Duration(value) * durationUnits[match[2]]
		remaining = remaining[len(match[0]):]
	}
	return total, nil
}

var bytesToken = regexp.MustCompile(`^([0-9]+)(KiB|MiB|GiB|TiB|PiB)$`)

func ParseBytes(raw string) (uint64, error) {
	match := bytesToken.FindStringSubmatch(strings.TrimSpace(raw))
	if match == nil {
		return 0, fmt.Errorf("bytes %q must use a base-2 suffix KiB, MiB, GiB, TiB, or PiB", raw)
	}
	value, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bytes %q is invalid", raw)
	}
	shift := map[string]uint{
		"KiB": 10,
		"MiB": 20,
		"GiB": 30,
		"TiB": 40,
		"PiB": 50,
	}[match[2]]
	return value << shift, nil
}

func ValidateTracing(endpoint string, sampleRatio float64) error {
	if sampleRatio < 0 || sampleRatio > 1 {
		return errors.New("tracing.sampleRatio must be in [0, 1]")
	}
	if endpoint == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || host == "" || port == "" {
		return errors.New("tracing.endpoint must be host:port when set")
	}
	return nil
}
