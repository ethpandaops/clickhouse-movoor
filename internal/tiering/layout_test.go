package tiering

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLayoutAndPartitions(t *testing.T) {
	frontier := DefaultTierSettings()
	frontier.Age = AgeSettings{Basis: AgeBasisFrontier, Field: "block_number", KeepLast: 100}
	layout, err := ParseLayout("db", "tbl", "(network_id, intDiv(block_number, 5000000))", frontier)
	require.NoError(t, err)
	require.Equal(t, []string{"network_id"}, layout.GroupColumns)
	require.Equal(t, "block_number", layout.AgeField)
	require.EqualValues(t, 5000000, layout.FrontierDivisor)

	value, err := layout.ParsePartition("('has\\'quote',-5)")
	require.NoError(t, err)
	require.Equal(t, "has'quote", value.GroupKey)
	require.Equal(t, int64(-5), value.AgeInteger)

	timeSettings := DefaultTierSettings()
	timeSettings.Age = AgeSettings{Basis: AgeBasisPartitionTime, OlderThan: Duration{Duration: 1}}
	timeLayout, err := ParseLayout("db", "tbl", "(bucket_id, toYYYYMM(event_time, 'Asia/Tokyo'))", timeSettings)
	require.NoError(t, err)
	require.Equal(t, "toYYYYMM", timeLayout.TimeFunction)
	require.Equal(t, "event_time", timeLayout.AgeField)
	require.Equal(t, "Asia/Tokyo", timeLayout.TimeZone)
	parsed, err := timeLayout.ParsePartition("('back\\\\slash',202604)")
	require.NoError(t, err)
	require.Equal(t, "back\\slash", parsed.GroupKey)
	require.Equal(t, "202604", parsed.AgeString)

	bareLayout, err := ParseLayout("db", "tbl", "toStartOfMonth(event_time)", timeSettings)
	require.NoError(t, err)
	bare, err := bareLayout.ParsePartition("2026-04-01")
	require.NoError(t, err)
	require.Empty(t, bare.GroupKey)
	require.Equal(t, "2026-04-01", bare.AgeString)

	// A table partitioned directly on a Date column needs no time function:
	// the sole bare column IS the age element, with toDate value semantics.
	logDateLayout, err := ParseLayout("db", "tbl", "LogDate", timeSettings)
	require.NoError(t, err)
	require.Equal(t, "toDate", logDateLayout.TimeFunction)
	require.Equal(t, "LogDate", logDateLayout.AgeField)
	require.Empty(t, logDateLayout.GroupColumns)
	logDate, err := logDateLayout.ParsePartition("2026-05-01")
	require.NoError(t, err)
	require.Equal(t, "2026-05-01", logDate.AgeString)

	// Multi-element keys disambiguate the date column via age.field.
	fieldSettings := DefaultTierSettings()
	fieldSettings.Age = AgeSettings{Basis: AgeBasisPartitionTime, OlderThan: Duration{Duration: 1}, Field: "LogDate"}
	groupedLayout, err := ParseLayout("db", "tbl", "(network_id, LogDate)", fieldSettings)
	require.NoError(t, err)
	require.Equal(t, "toDate", groupedLayout.TimeFunction)
	require.Equal(t, "LogDate", groupedLayout.AgeField)
	require.Equal(t, []string{"network_id"}, groupedLayout.GroupColumns)
}

func TestParseLayoutErrors(t *testing.T) {
	settings := DefaultTierSettings()
	settings.Age = AgeSettings{Basis: AgeBasisFrontier, Field: "block_number", KeepLast: 1}
	for _, tt := range []struct {
		key  string
		want string
	}{
		{key: "tuple()", want: "tuple()"},
		{key: "(network_id, intDiv(other, 100))", want: "field mismatch"},
		{key: "(network_id, intDiv(block_number, 100), intDiv(block_number, 10))", want: "exactly one"},
		{key: "(network_id, cityHash64(block_number))", want: "not a supported"},
		{key: "(network_id,, intDiv(block_number, 100))", want: "empty tuple"},
	} {
		_, err := ParseLayout("db", "tbl", tt.key, settings)
		require.ErrorContains(t, err, tt.want)
	}

	timeSettings := DefaultTierSettings()
	timeSettings.Age = AgeSettings{Basis: AgeBasisPartitionTime, OlderThan: Duration{Duration: 1}}
	_, err := ParseLayout("db", "tbl", "(network_id, block_number)", timeSettings)
	require.ErrorContains(t, err, "exactly one")
	_, err = ParseLayout("db", "tbl", "(network_id, toYYYYMM(ts, timezone_column))", timeSettings)
	require.ErrorContains(t, err, "not a supported")
}

func TestPartitionLiteralSyntaxAndTupleParser(t *testing.T) {
	require.NoError(t, ValidatePartitionLiteralSyntax("plain string, even with commas"))
	require.NoError(t, ValidatePartitionLiteralSyntax("('a,b',0)"))
	require.Error(t, ValidatePartitionLiteralSyntax(""))
	require.Error(t, ValidatePartitionLiteralSyntax("('oops)"))

	got, err := parseTupleLiteral("('line\\nfeed','tab\\tchar','raw\\x',42)")
	require.NoError(t, err)
	require.Equal(t, []string{"line\nfeed", "tab\tchar", "rawx", "42"}, got)
	_, err = parseTupleLiteral("(,)")
	require.Error(t, err)
	_, err = parseTupleLiteral("(1,)")
	require.Error(t, err)
	_, err = parseTupleLiteral("1,2")
	require.Error(t, err)
	_, _, err = readQuotedTupleString("'unterminated")
	require.Error(t, err)
}

func TestSplitTopLevelAndIdentifiers(t *testing.T) {
	parts, err := splitTopLevel("a, intDiv(block_number, 100), toYYYYMM(ts, 'UTC')")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "intDiv(block_number, 100)", "toYYYYMM(ts, 'UTC')"}, parts)
	_, err = splitTopLevel("a, (broken")
	require.Error(t, err)
	_, err = splitTopLevel("a,,b")
	require.Error(t, err)
	require.Equal(t, 1, topLevelComma("a,b"))
	require.Equal(t, -1, topLevelComma("'a,b'"))
	require.True(t, isBareIdentifier("_ok9"))
	require.False(t, isBareIdentifier("9bad"))
	require.False(t, isBareIdentifier("bad-name"))
	require.Equal(t, "UTC", parseTimezoneArgument("'UTC'"))
	require.Empty(t, parseTimezoneArgument("UTC"))
	require.Empty(t, classifyTimeLayoutElement("toYYYYMM(").timeFunction)
	require.Empty(t, classifyTimeLayoutElement("toYYYYMM(cityHash64(ts))").timeFunction)
	require.Empty(t, classifyTimeLayoutElement("toYYYYMM(ts, timezone_column)").timeFunction)
}
