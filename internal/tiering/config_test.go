package tiering

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConfigDefaultsCloneAndValidation(t *testing.T) {
	cfg := DefaultConfig()
	require.Equal(t, ModePlan, cfg.Mode)
	require.Equal(t, DefaultInterval, cfg.Interval.Duration)
	require.NoError(t, cfg.Validate(time.Now().Add(time.Hour)))

	settings := DefaultTierSettings()
	settings.Age = AgeSettings{Basis: AgeBasisFrontier, Field: "block_number", KeepLast: 100}
	settings.ExcludePartitions = []string{"abc"}
	clone := settings.Clone()
	settings.ExcludePartitions[0] = "mutated"
	require.Equal(t, []string{"abc"}, clone.ExcludePartitions)
	require.NoError(t, clone.Validate("tier", true))
}

func TestTierSettingsCloneCoversReferenceTypedFields(t *testing.T) {
	// Primary, structural guard for Clone(): every reference-typed field
	// (slice/map/ptr/chan) reachable from TierSettings must be deep-copied by
	// Clone(). A new one must be added to Clone() AND this allowlist, or it
	// silently aliases between a settings value and its clone.
	allow := map[string]struct{}{
		"TierSettings.ExcludePartitions": {},
	}
	assertNoUnhandledReferenceFields(t, reflect.TypeFor[TierSettings](), allow)
}

func assertNoUnhandledReferenceFields(t *testing.T, typ reflect.Type, allow map[string]struct{}) {
	t.Helper()
	for field := range typ.Fields() {
		path := typ.Name() + "." + field.Name
		kind := field.Type.Kind()
		if kind == reflect.Struct {
			assertNoUnhandledReferenceFields(t, field.Type, allow)
			continue
		}
		if kind == reflect.Slice || kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Chan {
			if _, ok := allow[path]; !ok {
				t.Fatalf("TierSettings reaches reference-typed field %q (%s) not handled by Clone(); deep-copy it in Clone() and add %q to the allowlist", path, kind, path)
			}
		}
	}
}

func TestResolveDefaultsFillsZeroValues(t *testing.T) {
	var cfg Config
	cfg.ResolveDefaults()
	require.Equal(t, ModePlan, cfg.Mode)
	require.Equal(t, DefaultInterval, cfg.Interval.Duration)
	require.Equal(t, DefaultMaxConcurrentPartitions, cfg.MaxConcurrentPartitions)
	require.Equal(t, DefaultMaxMovesPerCycle, cfg.Safety.MaxMovesPerCycle)
	require.Equal(t, DefaultMaxBytesInFlight, cfg.Safety.MaxBytesInFlight.Value)
	require.Equal(t, DefaultMaxBytesPerDay, cfg.Safety.MaxBytesPerDay.Value)
	require.Equal(t, DefaultDiffBreakerMaxPartitions, cfg.Safety.DiffBreaker.MaxPartitions)
	require.InEpsilon(t, DefaultDiffBreakerMaxTableFraction, cfg.Safety.DiffBreaker.MaxTableFraction, 0)

	var settings TierSettings
	settings.ResolveDefaults()
	require.Equal(t, DefaultTargetDisk, settings.TargetDisk)
	require.Equal(t, DefaultQuietFor, settings.QuietFor.Duration)
	require.Equal(t, SealedSignalAuto, settings.SealedSignal)
	require.Equal(t, DefaultTierFrozenAfter, settings.TierFrozenAfter.Duration)
	require.Equal(t, DefaultOptimizeToParts, settings.OptimizeToParts)
	require.Equal(t, DefaultOptimizeSkipAboveBytes, settings.OptimizeSkipAboveBytes.Value)
	require.Equal(t, DefaultOptimizeStallAfter, settings.OptimizeStallAfter.Duration)
	require.Equal(t, ResplitStrategyRemerge, settings.Resplit.Strategy)
	require.Equal(t, DefaultResplitQuietFor, settings.Resplit.QuietFor.Duration)
	require.Equal(t, DefaultResplitFragmentParts, settings.Resplit.FragmentAbovePartCount)
}

func TestConfigValidationErrors(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{name: "mode", mut: func(c *Config) { c.Mode = "bad" }, want: "tiering.mode"},
		{name: "interval", mut: func(c *Config) { c.Interval = Duration{} }, want: "interval"},
		{name: "concurrency", mut: func(c *Config) { c.MaxConcurrentPartitions = -1 }, want: "maxConcurrentPartitions"},
		{name: "observations", mut: func(c *Config) { c.MaxConcurrentObservations = -1 }, want: "maxConcurrentObservations"},
		{name: "moves", mut: func(c *Config) { c.Safety.MaxMovesPerCycle = -1 }, want: "maxMovesPerCycle"},
		{name: "bytes in flight", mut: func(c *Config) { c.Safety.MaxBytesInFlight = Bytes{} }, want: "maxBytesInFlight"},
		{name: "bytes per day", mut: func(c *Config) { c.Safety.MaxBytesPerDay = Bytes{} }, want: "maxBytesPerDay"},
		{name: "breaker partitions", mut: func(c *Config) { c.Safety.DiffBreaker.MaxPartitions = 0 }, want: "maxPartitions"},
		{name: "breaker fraction low", mut: func(c *Config) { c.Safety.DiffBreaker.MaxTableFraction = 0 }, want: "maxTableFraction"},
		{name: "breaker fraction high", mut: func(c *Config) { c.Safety.DiffBreaker.MaxTableFraction = 2 }, want: "maxTableFraction"},
		{name: "override partitions", mut: func(c *Config) {
			c.Safety.DiffBreaker.Override = &DiffBreakerOverride{MaxTableFraction: 1, Expires: now.Add(time.Hour)}
		}, want: "override.maxPartitions"},
		{name: "override fraction", mut: func(c *Config) {
			c.Safety.DiffBreaker.Override = &DiffBreakerOverride{MaxPartitions: 1, MaxTableFraction: 2, Expires: now.Add(time.Hour)}
		}, want: "override.maxTableFraction"},
		{name: "override expires", mut: func(c *Config) {
			c.Safety.DiffBreaker.Override = &DiffBreakerOverride{MaxPartitions: 1, MaxTableFraction: 1, Expires: now.Add(-time.Hour)}
		}, want: "override.expires"},
		{name: "default exclusions", mut: func(c *Config) { c.Defaults.ExcludePartitions = []string{"abc"} }, want: "defaults.excludePartitions"},
		{name: "default validation", mut: func(c *Config) { c.Defaults.QuietFor = Duration{Duration: -time.Second} }, want: "tiering.defaults.quietFor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.mut(&cfg)
			require.ErrorContains(t, cfg.Validate(now), tt.want)
		})
	}
}

func TestTierSettingsValidationErrors(t *testing.T) {
	base := DefaultTierSettings()
	base.Age = AgeSettings{Basis: AgeBasisPartitionTime, OlderThan: Duration{Duration: time.Hour}}
	tests := []struct {
		name string
		mut  func(*TierSettings)
		want string
	}{
		{name: "mode", mut: func(s *TierSettings) { s.Mode = "bad" }, want: "mode"},
		{name: "target", mut: func(s *TierSettings) { s.TargetDisk = "" }, want: "targetDisk"},
		{name: "quiet", mut: func(s *TierSettings) { s.QuietFor = Duration{} }, want: "quietFor"},
		{name: "signal", mut: func(s *TierSettings) { s.SealedSignal = "bad" }, want: "sealedSignal"},
		{name: "frozen", mut: func(s *TierSettings) { s.TierFrozenAfter = Duration{Duration: time.Second} }, want: "tierFrozenAfter"},
		{name: "parts", mut: func(s *TierSettings) { s.OptimizeToParts = 0 }, want: "optimizeToParts"},
		{name: "stall", mut: func(s *TierSettings) { s.OptimizeStallAfter = Duration{} }, want: "optimizeStallAfter"},
		{name: "resplit strategy", mut: func(s *TierSettings) { s.Resplit.Strategy = "bad" }, want: "strategy"},
		{name: "optimizeOn enum", mut: func(s *TierSettings) { s.OptimizeOn = "bad" }, want: "optimizeOn"},
		{name: "optimizeOn cold with skipOptimize", mut: func(s *TierSettings) {
			s.OptimizeOn = OptimizeOnCold
			s.SkipOptimize = true
		}, want: "meaningless"},
		{name: "resplit quiet", mut: func(s *TierSettings) { s.Resplit.QuietFor = Duration{Duration: time.Second} }, want: "resplit.quietFor"},
		{name: "resplit fragments", mut: func(s *TierSettings) { s.Resplit.FragmentAbovePartCount = 0 }, want: "fragmentAbovePartCount"},
		{name: "age required", mut: func(s *TierSettings) { s.Age = AgeSettings{} }, want: "age.basis is required"},
		{name: "age basis", mut: func(s *TierSettings) { s.Age.Basis = "bad" }, want: "age.basis"},
		{name: "older than", mut: func(s *TierSettings) { s.Age = AgeSettings{Basis: AgeBasisPartitionTime} }, want: "olderThan"},
		{name: "frontier field", mut: func(s *TierSettings) { s.Age = AgeSettings{Basis: AgeBasisFrontier, KeepLast: 1} }, want: "field"},
		{name: "frontier keep", mut: func(s *TierSettings) { s.Age = AgeSettings{Basis: AgeBasisFrontier, Field: "x"} }, want: "keepLast"},
		{name: "empty exclude", mut: func(s *TierSettings) { s.ExcludePartitions = []string{""} }, want: "excludePartitions[0]"},
		{name: "bad exclude", mut: func(s *TierSettings) { s.ExcludePartitions = []string{"('unterminated)"} }, want: "unterminated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := base.Clone()
			tt.mut(&settings)
			require.ErrorContains(t, settings.Validate("tier", true), tt.want)
		})
	}
	require.NoError(t, DefaultTierSettings().Validate("defaults", false))
}

func TestScalarParsingAndYAML(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"10ms":   10 * time.Millisecond,
		"2s":     2 * time.Second,
		"3m":     3 * time.Minute,
		"4h":     4 * time.Hour,
		"5d":     5 * 24 * time.Hour,
		"6w":     6 * 7 * 24 * time.Hour,
		"1y":     365 * 24 * time.Hour,
		"1h30m":  time.Hour + 30*time.Minute,
		"2w3d":   2*7*24*time.Hour + 3*24*time.Hour,
		"1h0m0s": time.Hour,
	} {
		got, err := ParseDuration(raw)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	_, err := ParseDuration("1q")
	require.Error(t, err)
	_, err = ParseDuration("-1s")
	require.Error(t, err)
	_, err = ParseDuration("1h30")
	require.Error(t, err)
	_, err = ParseDuration("")
	require.Error(t, err)

	for raw, want := range map[string]uint64{
		"1KiB": 1 << 10,
		"2MiB": 2 << 20,
		"3GiB": 3 << 30,
		"4TiB": 4 << 40,
		"5PiB": 5 << 50,
	} {
		got, parseErr := ParseBytes(raw)
		require.NoError(t, parseErr)
		require.Equal(t, want, got)
	}
	_, err = ParseBytes("1GB")
	require.Error(t, err)

	var decoded struct {
		Mode     Mode            `yaml:"mode"`
		Basis    AgeBasis        `yaml:"basis"`
		Strategy ResplitStrategy `yaml:"strategy"`
		Signal   SealedSignal    `yaml:"signal"`
		Optimize OptimizeSide    `yaml:"optimize"`
		Duration Duration        `yaml:"duration"`
		Bytes    Bytes           `yaml:"bytes"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("mode: enforce\nbasis: frontier\nstrategy: append\nsignal: partLog\noptimize: cold\nduration: 2w\nbytes: 9MiB\n"), &decoded))
	require.Equal(t, ModeEnforce, decoded.Mode)
	require.Equal(t, AgeBasisFrontier, decoded.Basis)
	require.Equal(t, ResplitStrategyAppend, decoded.Strategy)
	require.Equal(t, SealedSignalPartLog, decoded.Signal)
	require.Equal(t, OptimizeOnCold, decoded.Optimize)
	require.Equal(t, 14*24*time.Hour, decoded.Duration.Duration)
	require.Equal(t, uint64(9<<20), decoded.Bytes.Value)
	require.Error(t, yaml.Unmarshal([]byte("mode: nope\n"), &decoded))
	require.Error(t, yaml.Unmarshal([]byte("basis: nope\n"), &decoded))
	require.Error(t, yaml.Unmarshal([]byte("strategy: nope\n"), &decoded))
	require.Error(t, yaml.Unmarshal([]byte("signal: nope\n"), &decoded))
	require.Error(t, yaml.Unmarshal([]byte("optimize: nope\n"), &decoded))
	require.NoError(t, yaml.Unmarshal([]byte("duration: null\nbytes: null\n"), &decoded))

	var d Duration
	require.NoError(t, d.UnmarshalYAML(&yaml.Node{Tag: "!!null"}))
	require.Error(t, d.UnmarshalYAML(&yaml.Node{Value: strings.Repeat("9", 400) + "s"}))
	var b Bytes
	require.NoError(t, b.UnmarshalYAML(&yaml.Node{Tag: "!!null"}))
	require.Error(t, b.UnmarshalYAML(&yaml.Node{Value: strings.Repeat("9", 400) + "PiB"}))
}

func TestValidateTracing(t *testing.T) {
	require.NoError(t, ValidateTracing("", 0))
	require.NoError(t, ValidateTracing("otel:4317", 1))
	require.ErrorContains(t, ValidateTracing("", -0.1), "sampleRatio")
	require.ErrorContains(t, ValidateTracing("", 1.1), "sampleRatio")
	require.ErrorContains(t, ValidateTracing("otel", 1), "host:port")
}
