package tiering

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLQuotingAndDiskHelpers(t *testing.T) {
	require.Equal(t, "`a``b`", QuoteIdent("a`b"))
	require.Equal(t, "`db`.`tbl`", QuoteQualified("db", "tbl"))
	require.Equal(t, "'it''s'", QuoteString("it's"))
	disks := []DiskPart{{Disk: "default", Parts: 1}, {Disk: "s3_cache", Parts: 2}}
	require.True(t, hasDisk(disks, "s3_cache"))
	require.False(t, hasDisk(disks, "missing"))
	require.False(t, allOnDisk(disks, "s3_cache"))
	require.True(t, allOnDisk([]DiskPart{{Disk: "s3_cache", Parts: 1}}, "s3_cache"))
	require.False(t, allOnDisk(nil, "s3_cache"))
}

func TestQuoteStringRoundTripsThroughParser(t *testing.T) {
	// The emitter and our query_log parser must agree: what QuoteString
	// renders, readSQLString must recover verbatim — including backslashes,
	// which ClickHouse interprets inside string literals.
	for _, value := range []string{
		"202406",
		"pid'quoted",
		`back\slash`,
		`trailing\`,
		`mix'\quote`,
		``,
	} {
		parsed, err := readSQLString(QuoteString(value))
		require.NoError(t, err, value)
		require.Equal(t, value, parsed)
	}
}
