package tiering

import "strings"

func QuoteIdent(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

func QuoteQualified(database string, table string) string {
	return QuoteIdent(database) + "." + QuoteIdent(table)
}

// QuoteString renders a ClickHouse single-quoted string literal. Backslashes
// must be escaped FIRST: ClickHouse interprets backslash escapes inside
// string literals (our own readSQLString models exactly that), so doubling
// quotes alone would corrupt values containing backslashes — partition IDs
// are opaque, so defend even though generated IDs are alphanumeric today.
func QuoteString(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, "'", "''")
	return "'" + escaped + "'"
}

func hasDisk(disks []DiskPart, target string) bool {
	for _, disk := range disks {
		if disk.Disk == target && disk.Parts > 0 {
			return true
		}
	}
	return false
}

func allOnDisk(disks []DiskPart, target string) bool {
	if len(disks) == 0 {
		return false
	}
	for _, disk := range disks {
		if disk.Parts > 0 && disk.Disk != target {
			return false
		}
	}
	return hasDisk(disks, target)
}
